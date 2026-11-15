package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
)

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

// An error from the registry stops a push; a check that says no refuses it.
func TestRegistryStopped(t *testing.T) {
	for _, err := range []error{registry.ErrUnavailable, registry.ErrRateLimited, registry.ErrUnauthorized} {
		if !registryStopped(fmt.Errorf("push: %w", err)) {
			t.Errorf("%v did not stop the push", err)
		}
	}
	if registryStopped(errors.New("app overlays are not published to a public registry")) {
		t.Error("a refusal stopped the push")
	}
}

// Without -y a registry removal only reports: would_remove and would_move. With
// -y it reports removed and moved. A manifest holding a tag this tool did not
// write is kept either way.
func TestFinishRemovalJSONNamesTheKeysByWhetherItActed(t *testing.T) {
	defer func(j bool, y bool) { registryRemoveJSON, utils.YesMode = j, y }(registryRemoveJSON, utils.YesMode)
	registryRemoveJSON = true
	plan := registry.RemovePlan{Kept: []registry.Held{{
		Desc: ocispec.Descriptor{Digest: "sha256:abcd"}, Tags: []string{"latest"},
	}}}

	for _, tc := range []struct {
		yes  bool
		want []string
	}{
		{false, []string{"would_remove", "would_move"}},
		{true, []string{"removed", "moved"}},
	} {
		utils.YesMode = tc.yes
		var out bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&out)
		if err := finishRemovalJSON(cmd, t.Context(), "ghcr.io/lab/cnt", nil, plan); err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Repository string                `json:"repository"`
			Kept       []report.RegistryKept `json:"kept"`
		}
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(out.Bytes(), &keys)
		for _, key := range tc.want {
			if string(keys[key]) != "[]" {
				t.Errorf("yes=%v: %s = %s, want []", tc.yes, key, keys[key])
			}
		}
		if doc.Repository != "ghcr.io/lab/cnt" || len(doc.Kept) != 1 || doc.Kept[0].Reason != report.KeptForeignTag {
			t.Errorf("yes=%v: document = %s", tc.yes, out.String())
		}
	}
}

// A pull prints one fetch step with the copy's keys and where it landed. One
// that failed prints the step as failed, and ends with the error.
func TestWritePullStep(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "star--2.7.11b.sqf")
	if err := os.WriteFile(dest, []byte("sqf"), 0o644); err != nil {
		t.Fatal(err)
	}
	found := resolvedRegistryArtifact{
		desc: ocispec.Descriptor{Digest: "sha256:abcd"},
		annotations: map[string]string{
			registry.AnnTitle: "star/2.7.11b", registry.AnnArch: "amd64",
			registry.AnnIdentityScheme: "script-identity-v1", registry.AnnIdentitySHA: "9f2c",
		},
	}
	decode := func(out *bytes.Buffer) report.Step {
		t.Helper()
		var doc struct {
			Steps []report.Step `json:"steps"`
		}
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Steps) != 1 {
			t.Fatalf("document = %s, %v", out.String(), err)
		}
		return doc.Steps[0]
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := writePullStep(cmd, found, "ghcr.io/lab/cnt", dest, nil); err != nil {
		t.Fatal(err)
	}
	step := decode(&out)
	if step.Action != report.ActionFetch || step.Outcome != report.OutcomeDone || step.Name != "star/2.7.11b" ||
		step.Endpoint != "ghcr.io/lab/cnt" || step.Digest != "sha256:abcd" || step.Identity.SHA256 != "9f2c" ||
		step.Path != dest || step.Size != 3 {
		t.Errorf("step = %+v", step)
	}

	out.Reset()
	boom := errors.New("registry is unavailable")
	if err := writePullStep(cmd, found, "ghcr.io/lab/cnt", dest, boom); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if step := decode(&out); step.Outcome != report.OutcomeFailed || step.Error != boom.Error() || step.Size != 0 {
		t.Errorf("failed step = %+v", step)
	}
}
