package lock

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/capsule"
	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// archBuild is recipe built for arch, with keys its own sources derive.
func archBuild(t *testing.T, name, recipe, arch string) (meta.Manifest, map[string][]byte) {
	t.Helper()
	manifest, files := recipeArtifact(t, name, recipe)
	manifest.Platform.Arch = arch
	derived, err := key.Generate(manifest, key.Sources{meta.RecipeFileName: []byte(recipe)})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Keys = derived.Keys()
	return manifest, files
}

func TestApplyAddsAnArchitectureBesideTheOthers(t *testing.T) {
	root := projectRoot(t)
	amd, amdFiles := archBuild(t, "star/2.7.11b", "echo amd\n", "amd64")
	arm, armFiles := archBuild(t, "star/2.7.11b", "echo arm\n", "arm64")
	amdPath := vendor(t, root, amd, amdFiles)

	l := New()
	if err := Apply(root, l, &Pinned{Request: "star/2.7.11b", Name: amd.Name, Arch: "amd64", Artifact: amdPath}); err != nil {
		t.Fatal(err)
	}
	// Vendored after the first apply, as a second machine's lock would be.
	armPath := vendor(t, root, arm, armFiles)
	if err := Apply(root, l, &Pinned{Request: "star/2.7.11b", Name: arm.Name, Arch: "arm64", Artifact: armPath}); err != nil {
		t.Fatal(err)
	}
	got := l.Pins["star/2.7.11b"].Artifacts
	if got["amd64"] != amdPath || got["arm64"] != armPath || len(got) != 2 {
		t.Errorf("artifacts = %v, want both architectures", got)
	}
}

// A different artifact name at the same key takes none of the old pin's architectures.
func TestApplyDropsArchitecturesOfAnotherName(t *testing.T) {
	root := projectRoot(t)
	old, oldFiles := archBuild(t, "ubuntu24/base", "echo old\n", "arm64")
	other, otherFiles := archBuild(t, "debian12/base", "echo other\n", "amd64")
	oldPath, otherPath := vendor(t, root, old, oldFiles), vendor(t, root, other, otherFiles)

	l := New()
	l.Pins[BaseKey] = PinEntry{Artifacts: map[string]string{"arm64": oldPath}}
	if err := Apply(root, l, &Pinned{Request: BaseKey, Name: other.Name, Arch: "amd64", Artifact: otherPath}); err != nil {
		t.Fatal(err)
	}
	if got := l.Pins[BaseKey].Artifacts; len(got) != 1 || got["amd64"] != otherPath {
		t.Errorf("artifacts = %v, want only the new name's", got)
	}
}

func TestStageEntryRefusesTheSameEntryForAnotherArchitecture(t *testing.T) {
	root := projectRoot(t)
	manifest, files := archBuild(t, "star/2.7.11b", "echo star\n", "amd64")
	vendor(t, root, manifest, files)

	other := manifest
	other.Platform.Arch = "arm64"
	data, err := marshalManifest(other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = StageEntry(root, entryNameOf(manifest), map[string][]byte{meta.FileName: data})
	if !errors.Is(err, ErrArchConflict) || !strings.Contains(err.Error(), "#ARCH: noarch") {
		t.Fatalf("error = %v, want the conflict and the way out", err)
	}
}

func TestVerifyAsksEveryCoveredArchitectureForAnArtifact(t *testing.T) {
	root := projectRoot(t)
	amd, files := archBuild(t, "star/2.7.11b", "echo amd\n", "amd64")
	amdPath := vendor(t, root, amd, files)
	portable, portableFiles := archBuild(t, "refdata/1.0", "echo data\n", meta.ArchNone)
	portablePath := vendor(t, root, portable, portableFiles)

	l := New()
	l.Archs = []string{"amd64", "arm64"}
	l.Pins["star/2.7.11b"] = PinEntry{Artifacts: map[string]string{"amd64": amdPath}}
	l.Pins["refdata/1.0"] = PinEntry{Artifacts: map[string]string{meta.ArchNone: portablePath}}

	_, problems := Verify(root, l)
	text := problemText(problems)
	if len(problems) != 1 || !strings.Contains(text, "star/2.7.11b: has no arm64 artifact") {
		t.Fatalf("problems = %s, want only the pin that lacks arm64", text)
	}
}

// A recipe that lists other architectures only has nothing to pin on this one.
func TestVerifyExcusesAPinWhoseRecipeLeavesTheArchitectureOut(t *testing.T) {
	root := projectRoot(t)
	amd, files := archBuild(t, "cellranger/9.0.1", "#ARCH:amd64\necho cellranger\n", "amd64")
	amdPath := vendor(t, root, amd, files)

	l := New()
	l.Archs = []string{"amd64", "arm64"}
	l.Pins["cellranger/9.0.1"] = PinEntry{Artifacts: map[string]string{"amd64": amdPath}}

	verified, problems := Verify(root, l)
	if len(problems) != 0 {
		t.Fatalf("the exclusion was reported as a problem: %s", problemText(problems))
	}
	gaps := verified.Select(l, "arm64").Gaps
	if len(gaps) != 1 || !gaps[0].Excluded || !strings.Contains(gaps[0].Reason, "builds on amd64 only") {
		t.Errorf("gaps = %#v, want one excluded gap saying where it builds", gaps)
	}
}

func TestReconcileRepinsForAHostTheKeyIsMissingOn(t *testing.T) {
	root := projectRoot(t)
	host := meta.NativeArch()
	other := "arm64"
	if host == "arm64" {
		other = "amd64"
	}
	built, files := archBuild(t, "star/2.7.11b", "echo other\n", other)
	builtPath := vendor(t, root, built, files)
	cell, cellFiles := archBuild(t, "cellranger/9.0.1", "#ARCH:"+other+"\necho cellranger\n", other)
	cellPath := vendor(t, root, cell, cellFiles)

	l := New()
	l.Pins["star/2.7.11b"] = PinEntry{Artifacts: map[string]string{other: builtPath}}
	l.Pins["cellranger/9.0.1"] = PinEntry{Artifacts: map[string]string{other: cellPath}}
	result := &ScanResult{Requests: []Request{requestFor(t, "star/2.7.11b"), requestFor(t, "cellranger/9.0.1")}}

	needPin := Reconcile(root, l, result)
	if len(needPin) != 1 || needPin[0].Key != "star/2.7.11b" {
		t.Fatalf("needPin = %v, want only the pin this host can build", needPin)
	}
	if len(l.Pins) != 2 {
		t.Errorf("pins = %v, want the other architecture's kept", l.Pins)
	}

	if rest := Reconcile(root, l, &ScanResult{}); len(rest) != 0 || len(l.Pins) != 0 {
		t.Errorf("an unrequested pin kept some architecture: %v", l.Pins)
	}
}

func requestFor(t *testing.T, value string) Request {
	t.Helper()
	request, reason := ParseDeclaration(value)
	if reason != "" {
		t.Fatal(reason)
	}
	return request
}

func marshalManifest(manifest meta.Manifest) ([]byte, error) {
	return json.MarshalIndent(manifest, "", "  ")
}

func entryNameOf(manifest meta.Manifest) string {
	return capsule.EntryName(manifest.Name, manifest.Keys.Identity.Digest())
}
