package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/registry"
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
