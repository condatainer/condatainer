package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

// A script outside any project keeps today's behaviour untouched.
func TestProjectRunContextIgnoresANonProjectScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	writeScript(t, dir, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	t.Chdir(dir)

	got, err := projectRunContext(context.Background(), script, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("context = %#v, want none outside a project", got)
	}
}

// A script the lock ignore list leaves out resolves its declarations as a script outside the project.
func TestProjectRunContextIgnoresAScriptTheProjectIgnores(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "legacy/run.sh", "#DEP: star/2.7.11b\nrun\n")
	writeScript(t, root, lock.DirName+"/ignore", "/legacy\n")

	got, err := projectRunContext(context.Background(), filepath.Join(root, "legacy/run.sh"), true)
	if err != nil || got != nil {
		t.Fatalf("context = %#v, %v; want none", got, err)
	}
}

// The project is the current directory, so naming a project's script from
// outside resolves by name exactly as an unlocked script does. This is what
// replaced --no-project: `cd` elsewhere is the opt-out.
func TestProjectRunContextIgnoresAScriptNamedFromOutside(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	t.Chdir(t.TempDir())

	got, err := projectRunContext(context.Background(), filepath.Join(root, "run.sh"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("context = %#v, want none from outside the root", got)
	}
}

// Only the directory holding cnt-lock/ is the root; a subdirectory is not.
func TestProjectRunContextFindsAProjectFromASubdirectory(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "scripts/run.sh", "#DEP: env.img\nrun\n")
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "scripts"))

	got, err := projectRunContext(context.Background(), "run.sh", true)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("a subdirectory of a project was not recognized as standing in it")
	}
	if got.Root != root {
		t.Fatalf("context.Root = %q, want %q", got.Root, root)
	}
}

// Inside a project an unsatisfiable declaration is fatal, and the message names
// the one remedy rather than leaving the reader to guess.
func TestProjectRunContextFailsWithTheRestoreRemedy(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	t.Chdir(root)

	_, err := projectRunContext(context.Background(), filepath.Join(root, "run.sh"), true)
	if err == nil {
		t.Fatal("a project with nothing selected was allowed to run")
	}
	if !strings.Contains(err.Error(), "project restore") {
		t.Errorf("error does not name the remedy: %v", err)
	}
	if !strings.Contains(err.Error(), "star/2.7.11b") {
		t.Errorf("error does not name the declaration: %v", err)
	}
}

// An unpinnable declaration is mounted as the literal path it names, so a
// project carrying only those runs with no selections at all.
func TestProjectRunContextMountsUnpinnableDeclarations(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: env.img\nrun\n")
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	got, err := projectRunContext(context.Background(), filepath.Join(root, "run.sh"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no project context inside a project")
	}
	want := filepath.Join(root, "env.img")
	if len(got.Overlays) != 1 || got.Overlays[0] != want {
		t.Fatalf("overlays = %v, want %q anchored on the root", got.Overlays, want)
	}
}

// The script may live anywhere; the root is what declarations anchor on. Run
// from the root, `run scripts/align.sh` is an ordinary project run.
func TestProjectRunContextAnchorsASubdirectoryScriptOnTheRoot(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "scripts/align.sh", "#DEP: overlays/tool.img\nrun\n")
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	got, err := projectRunContext(context.Background(), filepath.Join(root, "scripts", "align.sh"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("a subdirectory script did not find the project above it")
	}
	want := filepath.Join(root, "overlays", "tool.img")
	if len(got.Overlays) != 1 || got.Overlays[0] != want {
		t.Fatalf("overlays = %v, want %q — the root, not the script directory", got.Overlays, want)
	}
}

func TestResolveDepsRefusesAVersionConstraint(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "run.sh", "#DEP: samtools>=1.20\nrun\n")

	if _, err := resolveDeps(context.Background(), filepath.Join(dir, "run.sh"), "run.sh"); !errors.Is(err, errRunAborted) {
		t.Fatalf("err = %v, want the run to abort on a constrained #DEP:", err)
	}
}

// A symlink to a script of the project is covered by the lock, and one to a script outside it is not.
func TestProjectRunContextFollowsASymlinkedScript(t *testing.T) {
	root, outside := newProject(t), t.TempDir()
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	writeScript(t, root, "real.sh", "#DEP: env.img\nrun\n")
	writeScript(t, outside, "away.sh", "#DEP: env.img\nrun\n")
	for name, target := range map[string]string{"in.sh": filepath.Join(root, "real.sh"), "out.sh": filepath.Join(outside, "away.sh")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	for name, covered := range map[string]bool{"in.sh": true, "out.sh": false} {
		got, err := projectRunContext(context.Background(), filepath.Join(root, name), false)
		if err != nil || (got != nil) != covered {
			t.Errorf("%s: context %#v, %v; covered should be %v", name, got, err, covered)
		}
	}
}
