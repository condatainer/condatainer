package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

// A line is anchored at the project root, wherever the command is typed.
func TestProjectIgnoreAnchorsPathsAtTheRoot(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "src/tmp/a.py", "")
	writeScript(t, root, "data/b.sh", "")
	t.Chdir(filepath.Join(root, "src"))

	if _, err := run(t, "project", "ignore", "tmp/*.py", "../data", "/notebooks/"); err != nil {
		t.Fatal(err)
	}
	if got, want := ignoreList(t, root), "/data\n/notebooks/\n/src/tmp/*.py\n"; got != want {
		t.Errorf("list = %q, want %q", got, want)
	}
	if out, err := run(t, "project", "ignore", "--list"); err != nil || out != "/data\n/notebooks/\n/src/tmp/*.py\n" {
		t.Errorf("--list = %q, %v", out, err)
	}
}

func TestProjectIgnoreRemovesALine(t *testing.T) {
	root := newProject(t)
	if _, err := run(t, "project", "ignore", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "project", "ignore", "--remove", "a"); err != nil {
		t.Fatal(err)
	}
	if got := ignoreList(t, root); got != "/b\n" {
		t.Errorf("list = %q", got)
	}
	if _, err := run(t, "project", "ignore", "--remove", "a"); err == nil {
		t.Error("removing a line that is not there succeeded")
	}
	if _, err := run(t, "project", "ignore", "--remove", "b"); err != nil {
		t.Fatal(err)
	}
	if got := ignoreList(t, root); got != "" {
		t.Errorf("an empty list leaves a file: %q", got)
	}
}

func TestProjectIgnoreSkipsWhatGitignoreCovers(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, ".gitignore", "venv/\n")
	if _, err := run(t, "project", "ignore", "venv/lib", "other"); err != nil {
		t.Fatal(err)
	}
	if got := ignoreList(t, root); got != "/other\n" {
		t.Errorf("list = %q", got)
	}
}

func TestProjectIgnoreRefusesAPathOutsideTheProject(t *testing.T) {
	newProject(t)
	_, err := run(t, "project", "ignore", "../elsewhere")
	if err == nil || !strings.Contains(err.Error(), "outside the project") {
		t.Errorf("err = %v", err)
	}
}

func ignoreList(t *testing.T, root string) string {
	t.Helper()
	text, err := os.ReadFile(lock.IgnorePath(root))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(text)
}
