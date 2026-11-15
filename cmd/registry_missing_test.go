package cmd

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/report"
)

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
	if got, err := parseMissingTypes("os, app,os", nil, true); err != nil || !slices.Equal(got, []artifact.Type{artifact.TypeOS, artifact.TypeApp}) {
		t.Errorf("--type alone = %v, %v", got, err)
	}
	if _, err := parseMissingTypes("data", nil, true); err == nil {
		t.Error("data was accepted: its templates have too many variants to list")
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
