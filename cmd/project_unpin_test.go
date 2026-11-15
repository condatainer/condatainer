package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

// A manual pin is what `project lock` cannot sweep, so `project unpin` is the
// only way it goes — and what it alone vendored goes with it.
func TestProjectUnpinRemovesAManualPinAndItsProvenance(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "ubuntu24/build-essential", "echo apt\n")
	l := lock.New()
	l.Pins["ubuntu24/build-essential"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := runConsole(t, "project", "unpin", "ubuntu24/build-essential")
	if err != nil {
		t.Fatalf("unpin failed: %v", err)
	}
	if !strings.Contains(out, artifact) {
		t.Errorf("the pruned artifact was not reported:\n%s", out)
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pins) != 0 {
		t.Fatalf("the pin survived: %#v", loaded.Pins)
	}
	if _, err := os.Stat(filepath.Join(lock.Dir(root), artifact)); !os.IsNotExist(err) {
		t.Errorf("its provenance was not pruned: %v", err)
	}
}

// A declared pin is removed by removing the declaration: unpinning one here
// would be undone by the next lock, possibly at a different identity.
func TestProjectUnpinRefusesADeclaredPin(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "project", "unpin", "star/2.7.11b")
	if err == nil {
		t.Fatal("a declared pin was unpinned")
	}
	if !strings.Contains(err.Error(), "project lock") {
		t.Errorf("the refusal does not name the way to remove it: %v", err)
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Pins["star/2.7.11b"]; !ok {
		t.Error("the refusal still removed the pin")
	}
}

func TestProjectUnpinRefusesWhatIsNotPinned(t *testing.T) {
	newProject(t)
	if _, err := run(t, "project", "unpin", "star/2.7.11b"); err == nil {
		t.Fatal("unpinning nothing succeeded")
	}
}
