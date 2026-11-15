package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

// A declared but needPin request leaves a valid partial lock and a nonzero
// exit: the lock is published, but never reported as complete.
func TestProjectLockPublishesPartiallyAndFails(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")

	out, err := run(t, "project", "lock", "--json")
	if err == nil {
		t.Fatal("project lock succeeded with an needPin request")
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Requests      []struct {
			Request  string `json:"request"`
			Pinnable bool   `json:"pinnable"`
			Artifact string `json:"artifact"`
		} `json:"requests"`
		Problems []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || len(doc.Requests) != 1 || doc.Requests[0].Request != "star/2.7.11b" ||
		!doc.Requests[0].Pinnable || doc.Requests[0].Artifact != "" || len(doc.Problems) == 0 {
		t.Errorf("document = %+v, want the unpinned request and why", doc)
	}
	if _, err := os.Stat(lock.FilePath(root)); err != nil {
		t.Fatalf("no lock was published: %v", err)
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatalf("the published lock does not load: %v", err)
	}
	if len(loaded.Pins) != 0 {
		t.Fatalf("selections = %#v, want none invented", loaded.Pins)
	}
}

// Reconcile drops a selection nothing declares any more. The lock is
// published right after Reconcile, before the base pin is even attempted, so
// this holds independent of whether a root can be derived.
func TestProjectLockDropsAStaleSelection(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	writeScript(t, root, "run.sh", "echo no declarations\n")

	// No base image is installed, so the run still fails deriving the base
	// pin — but that failure is reported alongside whatever Reconcile already
	// published, not instead of it.
	if _, err := run(t, "project", "lock"); err == nil {
		t.Fatal("project lock succeeded with no base image installed")
	}
	loaded, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pins) != 0 {
		t.Fatalf("a selection nothing declares survived: %#v", loaded.Pins)
	}
	if _, err := os.Stat(filepath.Join(lock.Dir(root), artifact)); !os.IsNotExist(err) {
		t.Errorf("its artifact was not pruned: %v", err)
	}
}

// Scanning a directory for declarations is what makes it a project, so lock has
// nothing to refuse. Every other subcommand acts on selections that must already
// exist, and must not seed a project from a mistyped path.
func TestOnlyLockCreatesAProject(t *testing.T) {
	prev := projectDir
	t.Cleanup(func() { projectDir = prev })
	projectDir = ""

	chdir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(wd) })
		// macOS hands out a symlinked temp path; Getwd resolves it.
		resolved, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}

	dir := chdir(t)
	root, err := projectRootOrInit(false)
	if err != nil || root != dir {
		t.Fatalf("projectRootOrInit = (%q, %v), want %q", root, err, dir)
	}

	if _, err := projectRoot(false); !errors.Is(err, lock.ErrNoProject) {
		t.Fatalf("projectRoot = %v, want ErrNoProject", err)
	}
	if _, err := os.Stat(filepath.Join(dir, lock.DirName)); !os.IsNotExist(err) {
		t.Error("resolving a root created cnt-lock/; only publishing should")
	}
}
