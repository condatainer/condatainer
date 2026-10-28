package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
)

func TestInferPushSource(t *testing.T) {
	good := &catalog.Source{Name: "lab", Desc: catalog.Descriptor{
		Source: "https://example.invalid/recipes/",
		OCI:    catalog.OCI{Registry: "registry.invalid/lab", Audience: "restricted"},
	}}
	for _, tc := range []struct {
		name    string
		cat     catalog.Catalog
		want    *catalog.Source
		wantErr string
	}{
		{"unique", catalog.Catalog{good}, good, ""},
		{"absent", nil, nil, "matches 0"},
		{"ambiguous", catalog.Catalog{good, &catalog.Source{Name: "other", Desc: good.Desc}}, nil, "matches 2"},
		{"invalid descriptor", catalog.Catalog{&catalog.Source{Name: "bad", Desc: good.Desc, DescriptorErr: errors.New("bad json")}}, nil, "invalid source descriptor"},
		{"stale", catalog.Catalog{&catalog.Source{Name: "old", Desc: good.Desc, Stale: true}}, nil, "unavailable or stale"},
		{"no registry", catalog.Catalog{&catalog.Source{Name: "noregistry", Desc: catalog.Descriptor{Source: good.Desc.Source}}}, nil, "no OCI registry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := inferPushSource("https://example.invalid/recipes", tc.cat)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("source = %p, error = %v", got, err)
			}
		})
	}
}

func TestRegistryCommandHasThePlannedSurface(t *testing.T) {
	cmd := newRegistryCommand()
	want := map[string]bool{
		"push": false, "pull": false, "tags": false,
		"resolve": false, "login": false, "logout": false, "list": false,
	}
	for _, child := range cmd.Commands() {
		if _, ok := want[child.Name()]; ok {
			want[child.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("registry command is missing %q", name)
		}
	}
}

func TestRegistryPushCompletionStopsAfterArtifact(t *testing.T) {
	got, directive := registryPushCompletion(nil, []string{"hello/1.0"}, "")
	if len(got) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("completion = %v, %v", got, directive)
	}
}

func TestCompletionScriptsRecognizeRegistryPushFZF(t *testing.T) {
	tests := []struct {
		name, input, marker string
		process             func(string) string
	}{
		{
			name: "bash", input: `__condatainer_debug "The completions are: ${out}"`,
			marker: `"$sub" == "registry" && "${words[2]}" == "push"`, process: postProcessBashCompletion,
		},
		{
			name: "zsh", input: `__condatainer_debug "completions: ${out}"`,
			marker: `"$sub" == "registry" && "${words[3]}" == "push"`, process: postProcessZshCompletion,
		},
		{
			name: "fish", input: `set -l results (eval $requestComp 2> /dev/null)`,
			marker: `test "$args[2]" = registry; and test "$args[3]" = push`, process: postProcessFishCompletion,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.process(tc.input)
			if !strings.Contains(got, tc.marker) || !strings.Contains(got, "Select overlay") {
				t.Fatalf("processed completion lacks registry push fzf hook:\n%s", got)
			}
		})
	}
}

func TestParseAudience(t *testing.T) {
	if _, err := parseAudience("private"); err == nil {
		t.Error("parseAudience accepted an unknown value")
	}
	if got, err := parseAudience("RESTRICTED"); err != nil || got != registry.Restricted {
		t.Errorf("parseAudience = (%q, %v)", got, err)
	}
}

func TestResolveRegistryEndpoint(t *testing.T) {
	cmd := newRegistryCommand()
	cmd.SetContext(t.Context())
	for _, tc := range []struct {
		name    string
		opts    registryOptions
		want    string
		wantErr string
	}{
		{"neither flag", registryOptions{}, "", "--registry or --source"},
		{"both flags", registryOptions{repository: "ghcr.io/lab/cnt", source: "lab"}, "", "not both"},
		{"a scheme and slash are trimmed", registryOptions{repository: "oci://ghcr.io/lab/cnt/"}, "ghcr.io/lab/cnt", ""},
		{"a host alone is no repository", registryOptions{repository: "ghcr.io"}, "", "not a registry/repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ep, err := resolveRegistryEndpoint(cmd, &tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || ep.repository != tc.want || ep.source != nil {
				t.Fatalf("endpoint = %+v, error = %v; want %s", ep, err, tc.want)
			}
		})
	}
}

func TestEndpointOfSource(t *testing.T) {
	good := &catalog.Source{Name: "lab", Desc: catalog.Descriptor{
		Source: "https://example.invalid/recipes",
		OCI:    catalog.OCI{Registry: "oci://registry.invalid/lab/cnt", Audience: "restricted"},
	}}
	noRegistry := &catalog.Source{Name: "bare", Desc: catalog.Descriptor{Source: good.Desc.Source}}
	noCollection := &catalog.Source{Name: "anon", Desc: catalog.Descriptor{OCI: good.Desc.OCI}}
	cat := catalog.Catalog{good, noRegistry, noCollection,
		&catalog.Source{Name: "old", Desc: good.Desc, Stale: true}}

	ep, err := endpointOfSource(t.Context(), cat, "lab")
	if err != nil || ep.repository != "registry.invalid/lab/cnt" || ep.source != good {
		t.Fatalf("endpoint = %+v, error = %v", ep, err)
	}
	// Only a push is matched to a collection, and a source that names none
	// matches any.
	if got := ep.pushCollection(); got != good.Desc.Source {
		t.Errorf("pushCollection = %q, want %q", got, good.Desc.Source)
	}
	anon, err := endpointOfSource(t.Context(), cat, "anon")
	if err != nil || anon.pushCollection() != "" {
		t.Errorf("a source naming no collection: (%q, %v), want it readable and matching any", anon.pushCollection(), err)
	}
	if got := (registryEndpoint{repository: "ghcr.io/lab/cnt"}).pushCollection(); got != "" {
		t.Errorf("a bare repository takes any collection, got %q", got)
	}
	for name, want := range map[string]string{
		"missing": "no configured source",
		"bare":    "no OCI registry",
		"old":     "unavailable or stale",
	} {
		if _, err := endpointOfSource(t.Context(), cat, name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("source %q: error = %v, want %q", name, err, want)
		}
	}
}

// An installed name/version is addressed by this system and infers where it
// publishes; a path the user pointed at does not, so it must say where it goes.
func TestFindRegistryArtifactSeparatesManagedFromPointedAt(t *testing.T) {
	// GlobalDataPaths caches the resolved search paths, so a test that ran earlier
	// may already have resolved them from the real environment — setting
	// XDG_DATA_HOME here would not be read. Pin the paths instead.
	images := filepath.Join(t.TempDir(), "images")
	if err := os.MkdirAll(images, 0o755); err != nil {
		t.Fatal(err)
	}
	prevPaths := config.GlobalDataPaths
	config.GlobalDataPaths.ImagesDirs = []string{images}
	t.Cleanup(func() { config.GlobalDataPaths = prevPaths })
	installed := filepath.Join(images, "hello--1.0.sqf")
	if err := os.WriteFile(installed, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "hello.sqf")
	if err := os.WriteFile(external, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findRegistryArtifact("hello/1.0")
	if err != nil || got.path != installed || !got.managed {
		t.Fatalf("name/version = %+v, error = %v; want %s managed", got, err, installed)
	}
	if got, err = findRegistryArtifact(external); err != nil || got.path != external || got.managed {
		t.Fatalf("path = %+v, error = %v; want %s unmanaged", got, err, external)
	}
	if _, err = findRegistryArtifact("absent/9.9"); err == nil {
		t.Error("a name matching nothing resolved anyway")
	}
}

// Inference reads the artifact's recorded build source, so without this a path
// pointed at would publish to its recipe collection's endpoint — which is the
// least likely destination for a one-off build, a mirror, or a project's image.
func TestRegistryPushDestinationRefusesToInferForAPath(t *testing.T) {
	cmd := newRegistryCommand()
	cmd.SetContext(t.Context())
	opts := &registryOptions{audience: string(registry.Public)}

	_, err := registryPushDestination(cmd, opts, registryArtifact{path: "/tmp/one-off.sqf"})
	if err == nil || !strings.Contains(err.Error(), "--registry") {
		t.Fatalf("error = %v, want a request for --registry", err)
	}
	if !strings.Contains(err.Error(), "name/version") {
		t.Errorf("the error does not say what would infer: %v", err)
	}

	// The same path with an explicit destination publishes, and never reads the
	// artifact to find one.
	opts.repository = "ghcr.io/lab/cnt"
	dest, err := registryPushDestination(cmd, opts, registryArtifact{path: "/tmp/one-off.sqf"})
	if err != nil || dest.repository != "ghcr.io/lab/cnt" || dest.audience != registry.Public || dest.collection != "" {
		t.Fatalf("explicit destination = (%+v, %v)", dest, err)
	}
}

func TestPushAudienceComesFromTheSource(t *testing.T) {
	push, _, err := newRegistryCommand().Find([]string{"push"})
	if err != nil {
		t.Fatal(err)
	}
	source := &catalog.Source{Name: "lab"}

	got, err := pushAudience(push, source, "restricted")
	if err != nil || got != registry.Restricted {
		t.Fatalf("source audience = (%q, %v), want restricted", got, err)
	}
	if err := push.Flags().Set("audience", "restricted"); err != nil {
		t.Fatal(err)
	}
	if _, err := pushAudience(push, source, "public"); err == nil || !strings.Contains(err.Error(), "--registry") {
		t.Errorf("--audience with a source = %v, want a refusal naming --registry", err)
	}
	if got, err := pushAudience(push, nil, "restricted"); err != nil || got != registry.Restricted {
		t.Errorf("--audience with --registry = (%q, %v), want restricted", got, err)
	}
}

func TestFrozenEnvironmentsStayOffSourcesAndOutOfLayers(t *testing.T) {
	if err := refuseFrozen(meta.Manifest{BuildType: meta.BuildTypeSnapshot}, "env.sqf"); err == nil {
		t.Error("a frozen environment was accepted for a source's registry")
	}
	if err := refuseFrozen(meta.Manifest{BuildType: meta.BuildTypeScript}, "star.sqf"); err != nil {
		t.Errorf("a script build was refused for a source's registry: %v", err)
	}

	frozen := map[string]string{registry.AnnIdentityScheme: string(key.PayloadTreeV1)}
	if err := requirePrefixForFrozen(frozen, ""); err == nil || !strings.Contains(err.Error(), "--prefix") {
		t.Errorf("a frozen pull without --prefix = %v, want a request for --prefix", err)
	}
	if err := requirePrefixForFrozen(frozen, "./env.sqf"); err != nil {
		t.Errorf("a frozen pull with --prefix = %v", err)
	}
	built := map[string]string{registry.AnnIdentityScheme: string(key.ScriptIdentityV1)}
	if err := requirePrefixForFrozen(built, ""); err != nil {
		t.Errorf("a script build pulled without --prefix = %v", err)
	}
}

func TestParseRegistryAddress(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want registry.Address
	}{
		{"star/2.7.11b", registry.Address{Name: "star/2.7.11b"}},
		{"star--2.7.11b", registry.Address{Name: "star/2.7.11b"}},
		{"star/2.7.11b@9f2c1ab", registry.Address{Name: "star/2.7.11b", Identity: "9f2c1ab"}},
		{"star/2.7.11b@script-identity-v1@sha256:9F2C1AB", registry.Address{Name: "star/2.7.11b", Identity: "9f2c1ab", Scheme: "script-identity-v1"}},
		{"star/2.7.11b:eq-e90d3b7714cf", registry.Address{Name: "star/2.7.11b", Equiv: "e90d3b7714cf"}},
	} {
		got, err := parseRegistryAddress(tc.spec, "")
		if err != nil || got != tc.want {
			t.Errorf("parseRegistryAddress(%q) = %+v, %v; want %+v", tc.spec, got, err, tc.want)
		}
	}
	if got, _ := parseRegistryAddress("star/2.7.11b", "arm64"); got.Arch != "arm64" {
		t.Errorf("--arch was dropped: %+v", got)
	}
	for _, bad := range []string{"", "  ", "star/2.7.11b:1.0", "star@", "star@zz"} {
		if _, err := parseRegistryAddress(bad, ""); err == nil {
			t.Errorf("parseRegistryAddress(%q) accepted a malformed address", bad)
		}
	}
}

func TestRegistryPullDestination(t *testing.T) {
	// The root dir is cached per process, so which tier is writable cannot be
	// pinned from here; the destination is asserted against whichever one is.
	images, err := config.GetWritableImagesDir()
	if err != nil {
		t.Skipf("no writable images directory: %v", err)
	}

	dest, err := registryPullDestination("", "hello/1.0", "")
	if err != nil || dest != filepath.Join(images, "hello--1.0.sqf") {
		t.Fatalf("published-name destination = (%q, %v)", dest, err)
	}
	dest, err = registryPullDestination(filepath.Join(t.TempDir(), "custom"), "hello/1.0", "")
	if err != nil || filepath.Base(dest) != "custom.sqf" {
		t.Fatalf("--prefix destination = (%q, %v)", dest, err)
	}
	dest, err = registryPullDestination(filepath.Join(t.TempDir(), "custom.v1"), "hello/1.0", "")
	if err != nil || filepath.Base(dest) != "custom.v1.sqf" {
		t.Errorf("extensionless dotted prefix = (%q, %v)", dest, err)
	}
	dest, err = registryPullDestination("", "", "hello/1.0")
	if err != nil || dest != filepath.Join(images, "hello--1.0.sqf") {
		t.Fatalf("destination from the published title = (%q, %v)", dest, err)
	}
	if _, err := registryPullDestination("", "", ""); err == nil {
		t.Error("a digest without a title guessed an install name")
	}
}

func TestRegistryPasswordFromStdin(t *testing.T) {
	cmd := newRegistryCommand()
	cmd.SetIn(bytes.NewBufferString("secret\n"))
	got, err := registryPassword(cmd, &registryOptions{passwordStdin: true})
	if err != nil || got != "secret" {
		t.Fatalf("registryPassword = (%q, %v)", got, err)
	}
	if _, err := registryPassword(cmd, &registryOptions{passwordStdin: true, password: "also"}); err == nil {
		t.Error("registryPassword accepted two password sources")
	}
}

func TestParseRemoveTarget(t *testing.T) {
	got, err := parseRemoveTarget("star/2.7.11b@9f2c1ab", "arm64")
	if err != nil || got != (registry.RemoveTarget{Name: "star/2.7.11b", Identity: "9f2c1ab", Arch: "arm64"}) {
		t.Errorf("name@identity = (%+v, %v)", got, err)
	}
	got, err = parseRemoveTarget("star--2.7.11b", "")
	if err != nil || got != (registry.RemoveTarget{Name: "star/2.7.11b"}) {
		t.Errorf("name = (%+v, %v), want every build", got, err)
	}
	if _, err := parseRemoveTarget("star/2.7.11b:eq-9f2c1ab4d3e8", ""); err == nil {
		t.Error("an equivalence key was taken as a build")
	}
}

func TestConfirmRemovalNeedsATerminalOrYes(t *testing.T) {
	cmd := newRegistryCommand()
	cmd.SetContext(t.Context())
	plan := registry.RemovePlan{Removes: []registry.Held{{Tags: []string{"star--1__amd64"}}}}

	prev := utils.YesMode
	t.Cleanup(func() { utils.YesMode = prev })

	utils.YesMode = false
	if ok, err := confirmRemoval(cmd, "ghcr.io/lab/cnt", plan); ok || err == nil {
		t.Errorf("without a terminal or -y = (%v, %v), want a refusal", ok, err)
	}
	utils.YesMode = true
	if ok, err := confirmRemoval(cmd, "ghcr.io/lab/cnt", plan); !ok || err != nil {
		t.Errorf("with -y = (%v, %v), want yes", ok, err)
	}
}

func TestSourceDeclaringMatchesTheRegistryOfASource(t *testing.T) {
	cat := catalog.Catalog{
		{Name: "lab", Desc: catalog.Descriptor{OCI: catalog.OCI{Registry: "oci://GHCR.io/lab/cnt/"}}},
		{Name: "bare"},
	}
	if got := sourceDeclaring(cat, "ghcr.io/lab/cnt"); got != "lab" {
		t.Errorf("source declaring ghcr.io/lab/cnt = %q, want lab", got)
	}
	if got := sourceDeclaring(cat, "ghcr.io/lab/project/cnt"); got != "" {
		t.Errorf("a project's repository = %q, want none", got)
	}
}

func TestRecipeCheckAsksTheSourceAndRefusesWhenItCannotSay(t *testing.T) {
	lab := t.TempDir()
	recipe := filepath.Join(lab, "recipes", "star", "2.7.11b")
	if err := os.MkdirAll(filepath.Dir(recipe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipe, []byte("#!/bin/bash\n#DESC: aligner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(t.Context(), []catalog.Spec{{Name: "lab", Base: lab}}, catalog.Cache{})
	if err != nil {
		t.Fatal(err)
	}
	has, err := recipeCheck(registryEndpoint{ctx: t.Context(), source: cat[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !has("star/2.7.11b") || has("old/1") {
		t.Errorf("star/2.7.11b = %v, old/1 = %v; want true, false", has("star/2.7.11b"), has("old/1"))
	}

	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	cat, err = catalog.Open(t.Context(), []catalog.Spec{{Name: "bare", Base: empty}}, catalog.Cache{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recipeCheck(registryEndpoint{ctx: t.Context(), source: cat[0]}); err == nil || !strings.Contains(err.Error(), "no recipes") {
		t.Errorf("a source with no recipes = %v, want a refusal", err)
	}
	if _, err := recipeCheck(registryEndpoint{ctx: t.Context()}); err == nil {
		t.Error("no source was accepted")
	}
}

func TestParseMissingArchs(t *testing.T) {
	got, err := parseMissingArchs("amd64, arm64,amd64")
	if err != nil || !slices.Equal(got, []string{"amd64", "arm64"}) {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := parseMissingArchs(""); err != nil || !slices.Equal(got, []string{meta.NativeArch()}) {
		t.Errorf("default = %v, %v", got, err)
	}
	if _, err := parseMissingArchs("amd64,riscv"); err == nil {
		t.Error("an unknown architecture was accepted")
	}
	if _, err := parseMissingArchs("noarch"); err == nil {
		t.Error("noarch was accepted: it is what a recipe is, not a machine")
	}
}

// A step is a published copy when its lookup pulled one, and a missing one when
// the lookup found nothing. Both carry the recipe's key and tag.
func TestCopyOfReadsTheLookup(t *testing.T) {
	equiv := meta.KeyRef{Scheme: "definition-equiv-v1", SHA256: "e90d3b7714cf0000"}
	identity := meta.KeyRef{Scheme: "definition-identity-v1", SHA256: "9f2c"}
	step := build.PlannedStep{Name: "ubuntu24/base", Type: artifact.TypeOS, Arch: "arm64", Registry: "ghcr.io/lab/cnt", Equiv: equiv}

	c, problem, err := copyOf(step)
	if err != nil || problem != "" || c.Published || c.Tag != "ubuntu24--base__eq-e90d3b7714cf__arm64" || c.Equiv != equiv || c.Digest != "" {
		t.Errorf("missing = %+v, %q, %v", c, problem, err)
	}

	step.Pull, step.Digest, step.Identity = true, "sha256:abcd", identity
	c, _, _ = copyOf(step)
	if !c.Published || c.Digest != "sha256:abcd" || c.Identity != identity || c.Name != "ubuntu24/base" || c.Arch != "arm64" || c.Type != artifact.TypeOS {
		t.Errorf("published = %+v", c)
	}
}

// What cannot be checked is a problem and leaves the others; a registry that
// cannot be reached is an error, since nothing is known to be missing.
func TestCopyOfReportsWhatCannotBeChecked(t *testing.T) {
	cases := map[string]build.PlannedStep{
		"no registry": {Name: "samtools/1.23", NoRegistry: "a Conda build has nothing published"},
		"no key":      {Name: "data/1", Registry: "ghcr.io/lab/cnt"},
	}
	for name, step := range cases {
		if _, problem, err := copyOf(step); problem == "" || err != nil {
			t.Errorf("%s: problem = %q, err = %v", name, problem, err)
		}
	}
	if _, _, err := copyOf(build.PlannedStep{Name: "ubuntu24/base", Registry: "ghcr.io/lab/cnt", Unreachable: true}); err == nil {
		t.Error("an unreachable registry was not an error")
	}
}

// Text lists the missing copies as name and architecture; --json writes every
// copy in the copies document.
func TestWriteCopies(t *testing.T) {
	copies := []report.Copy{
		{Overlay: report.Overlay{Name: "a/1", Arch: "amd64"}, Published: true},
		{Overlay: report.Overlay{Name: "a/1", Arch: "arm64"}},
	}
	var text bytes.Buffer
	if err := writeCopies(&text, "ghcr.io/lab/cnt", copies, nil, false); err != nil || text.String() != "a/1 arm64\n" {
		t.Errorf("text = %q, %v", text.String(), err)
	}

	var doc bytes.Buffer
	if err := writeCopies(&doc, "ghcr.io/lab/cnt", copies, nil, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		SchemaVersion int           `json:"schema_version"`
		Repository    string        `json:"repository"`
		Overlays      []report.Copy `json:"overlays"`
		Problems      []any         `json:"problems"`
	}
	if err := json.Unmarshal(doc.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.Repository != "ghcr.io/lab/cnt" || len(got.Overlays) != 2 || got.Problems == nil {
		t.Errorf("document = %s", doc.String())
	}
}

// Types are all of them unless --type narrows a pattern, and --type beside an
// exact name is refused.
func TestParseMissingTypes(t *testing.T) {
	if got, err := parseMissingTypes("", []string{"tools/*"}, false); err != nil || got != nil {
		t.Errorf("a pattern alone = %v, %v", got, err)
	}
	if got, err := parseMissingTypes("app, data,app", nil, true); err != nil || !slices.Equal(got, []artifact.Type{artifact.TypeApp, artifact.TypeData}) {
		t.Errorf("--type alone = %v, %v", got, err)
	}
	if _, err := parseMissingTypes("os", []string{"ubuntu24/base"}, true); err == nil {
		t.Error("--type was accepted beside an exact name")
	}
	if _, err := parseMissingTypes("env", nil, true); err == nil {
		t.Error("env was accepted: no recipe makes one")
	}
}

// * is any run of characters, / included, and ? is one; the rest is literal.
func TestPatternMatcher(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"ubuntu24/r/*", "ubuntu24/r/4.6.1", true},
		{"ubuntu24/r/*", "ubuntu24/r-build-essential", false},
		{"ubuntu24/r/4.?.1", "ubuntu24/r/4.6.1", true},
		{"ubuntu24/r/4.?.1", "ubuntu24/r/4.16.1", false},
		{"ubuntu24/*", "ubuntu24/r/4.6.1", true},
		{"a.b*", "axb1", false},
	} {
		if got := patternMatcher(tc.pattern).MatchString(tc.name); got != tc.want {
			t.Errorf("%q against %q = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// Each tag keeps its spelling and says what it is; one this tool did not write
// has no name or key.
func TestTagsDocReadsEachTag(t *testing.T) {
	doc := tagsDoc("ghcr.io/lab/cnt", []string{"star--2.7.11b__eq-e90d3b7714cf__amd64", "latest"})
	if doc.Repository != "ghcr.io/lab/cnt" || len(doc.Tags) != 2 {
		t.Fatalf("doc = %+v", doc)
	}
	if got := doc.Tags[0]; got.Kind != registry.TagEquiv || got.Name != "star/2.7.11b" || got.Key != "e90d3b7714cf" || got.Arch != "amd64" {
		t.Errorf("equiv tag = %+v", got)
	}
	if got := doc.Tags[1]; got != (report.Tag{Tag: "latest", Kind: registry.TagOther}) {
		t.Errorf("other tag = %+v", got)
	}
}

// A resolved copy reads its name, architecture and keys from the manifest
// annotations, and names the tag the address asked for.
func TestResolutionNamesTheTagAsked(t *testing.T) {
	equiv := meta.KeyRef{Scheme: "script-equiv-v1", SHA256: "e90d3b7714cf0000"}
	identity := meta.KeyRef{Scheme: "script-identity-v1", SHA256: "9f2c1ab4d3e80000"}
	found := resolvedRegistryArtifact{
		desc: ocispec.Descriptor{Digest: "sha256:abcd"},
		annotations: map[string]string{
			registry.AnnTitle: "star/2.7.11b", registry.AnnArch: "amd64",
			registry.AnnIdentityScheme: identity.Scheme, registry.AnnIdentitySHA: identity.SHA256,
			registry.AnnEquivScheme: equiv.Scheme, registry.AnnEquivSHA: equiv.SHA256,
		},
	}
	for spec, tag := range map[string]string{
		"star/2.7.11b":                 "star--2.7.11b__amd64",
		"star/2.7.11b@9f2c1ab":         "star--2.7.11b__9f2c1ab4d3e8__amd64",
		"star/2.7.11b:eq-e90d3b7714cf": "star--2.7.11b__eq-e90d3b7714cf__amd64",
		"":                             "",
	} {
		got := resolution(found, spec, "amd64").Overlay
		if got.Tag != tag || got.Name != "star/2.7.11b" || got.Arch != "amd64" || got.Digest != "sha256:abcd" || got.Identity != identity || got.Equiv != equiv {
			t.Errorf("%q: resolved = %+v, want tag %q", spec, got, tag)
		}
	}
}
