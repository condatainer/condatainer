package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project"
)

// Nothing to relocate to is a no-op: the process stays where it is.
func TestApplyProjectRelocationNoopWithNoDir(t *testing.T) {
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := applyProjectRelocation(""); err != nil {
		t.Fatal(err)
	}
	after, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("cwd changed from %q to %q with no --project", before, after)
	}
}

// --project changes into the named directory.
func TestApplyProjectRelocationChangesDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())

	if err := applyProjectRelocation(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		// macOS temp dirs resolve through a symlink, so compare the
		// symlink-resolved form rather than the raw string.
		resolved, _ := filepath.EvalSymlinks(dir)
		if got != resolved {
			t.Fatalf("cwd = %q, want %q", got, dir)
		}
	}
}

// --project and --no-project contradict each other.
func TestApplyProjectRelocationRefusesWithNoProject(t *testing.T) {
	noProjectRequested = true
	t.Cleanup(func() { noProjectRequested = false })

	if err := applyProjectRelocation(t.TempDir()); err == nil {
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
