package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
)

func projectRootDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, lock.DirName), 0o775); err != nil {
		t.Fatal(err)
	}
	return root
}

// --project names the project and leaves the working directory where it is, so a relative path, the log folder and a job's work directory do not move.
func TestApplyProjectFlagKeepsTheWorkingDirectory(t *testing.T) {
	root, elsewhere := projectRootDir(t), t.TempDir()
	t.Chdir(elsewhere)
	t.Cleanup(func() { projectRootFlag = "" })
	before, _ := os.Getwd()

	if err := applyProjectFlag(root); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Getwd(); after != before {
		t.Errorf("cwd changed from %q to %q", before, after)
	}
	standing, err := standingProjectHere()
	if err != nil || standing == nil || standing.Root != root {
		t.Errorf("standing = %#v, %v; want the project at %s", standing, err, root)
	}
}

// Without the flag the project is the nearest one above the current directory, or none.
func TestApplyProjectFlagEmptyUsesTheCurrentDirectory(t *testing.T) {
	root := projectRootDir(t)
	inside := filepath.Join(root, "steps")
	if err := os.MkdirAll(inside, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := applyProjectFlag(""); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inside)
	if standing, err := standingProjectHere(); err != nil || standing == nil || standing.Root != root {
		t.Errorf("inside a project: %#v, %v", standing, err)
	}
	t.Chdir(t.TempDir())
	if standing, err := standingProjectHere(); err != nil || standing != nil {
		t.Errorf("outside a project: %#v, %v", standing, err)
	}
}

// A directory that is not itself a project root is refused, whether or not a project lies above it.
func TestApplyProjectFlagRefusesWhatIsNotAProjectRoot(t *testing.T) {
	root := projectRootDir(t)
	inside := filepath.Join(root, "steps")
	if err := os.MkdirAll(inside, 0o775); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { projectRootFlag = "" })
	for _, dir := range []string{t.TempDir(), inside} {
		if err := applyProjectFlag(dir); !errors.Is(err, lock.ErrNoProject) {
			t.Errorf("%s: err = %v, want ErrNoProject", dir, err)
		}
	}
}

// --project and --no-project contradict each other.
func TestApplyProjectFlagRefusesWithNoProject(t *testing.T) {
	noProjectRequested = true
	t.Cleanup(func() { noProjectRequested = false })

	if err := applyProjectFlag(projectRootDir(t)); err == nil {
		t.Fatal("--project and --no-project were both accepted")
	}
}

// announceProject fires once per invocation: a second call is a no-op, so
// resolving overlays and the root in the same run does not print twice.
func TestAnnounceProjectFiresOnce(t *testing.T) {
	projectAnnounced = false
	t.Cleanup(func() { projectAnnounced = false })

	announceProject("/some/project")
	if !projectAnnounced {
		t.Fatal("announceProject did not mark itself announced")
	}
	announceProject("/some/project")
	if !projectAnnounced {
		t.Fatal("a second call cleared the announced flag")
	}
}

// In a project every dependency shows the full name and the identity mounted, and a mark when it is not exactly pinned.
func TestPrintMountsInAProject(t *testing.T) {
	mounts := []project.Mount{
		{Request: "star", Name: "star/2.7.11b", Identity: "sha256:aaaa1111bbbb2222"},
		{Request: "fastp/1.1.0", Name: "fastp/1.1.0", Live: true},
		{Request: "bwa/0.7", Name: "bwa/0.7", Identity: "sha256:bbbb1111bbbb2222", Found: "sha256:cccc1111dddd2222"},
		{Request: "path:env.sqf", Path: "/p/env.sqf"},
	}
	out, _ := stderrOf(t, func() error { printMounts(mounts, true); return nil })
	for _, want := range []string{"star → star/2.7.11b@aaaa1111bbbb\n", "fastp/1.1.0 → fastp/1.1.0  (not pinned)", "bwa/0.7 → bwa/0.7@cccc1111dddd  (equivalent build)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "env.sqf") || strings.Contains(out, "/o/") {
		t.Errorf("a file or a path was printed: %q", out)
	}
}

// Outside a project only a name that changed is shown.
func TestPrintMountsOutsideAProject(t *testing.T) {
	mounts := []project.Mount{
		{Request: "samtools", Name: "samtools/1.22", Live: true},
		{Request: "fastp/1.1.0", Name: "fastp/1.1.0", Live: true},
	}
	out, _ := stderrOf(t, func() error { printMounts(mounts, false); return nil })
	if !strings.Contains(out, "samtools → samtools/1.22\n") || strings.Contains(out, "fastp") {
		t.Errorf("got %q", out)
	}
}
