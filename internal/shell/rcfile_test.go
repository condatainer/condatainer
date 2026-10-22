package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testBlock = StartMarker + "\nline one\n" + EndMarker + "\n"

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApplyBlockCreatesAppendsAndReplacesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")

	c, err := ApplyBlock(path, testBlock)
	if err != nil || c.Action != Created {
		t.Fatalf("new file: %v, %v", c.Action, err)
	}
	if got := readFile(t, path); got != testBlock {
		t.Fatalf("new file = %q", got)
	}

	writeTestFile(t, path, "export A=1\n")
	if c, _ = ApplyBlock(path, testBlock); c.Action != Appended {
		t.Fatalf("existing file: %v", c.Action)
	}
	if got := readFile(t, path); got != "export A=1\n\n"+testBlock {
		t.Fatalf("appended = %q", got)
	}

	if c, _ = ApplyBlock(path, testBlock); c.Action != Unchanged {
		t.Fatalf("second run: %v", c.Action)
	}

	writeTestFile(t, path, "before\n"+testBlock+"after\n")
	next := StartMarker + "\nline two\n" + EndMarker + "\n"
	if c, _ = ApplyBlock(path, next); c.Action != Updated {
		t.Fatalf("replace: %v", c.Action)
	}
	if got := readFile(t, path); got != "before\n"+next+"after\n" {
		t.Fatalf("replaced = %q", got)
	}
}

func TestApplyBlockLoneMarkerIsLeftAndNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	writeTestFile(t, path, "a\n"+StartMarker+"\nb\n")

	c, err := ApplyBlock(path, testBlock)
	if err != nil || c.Action != Appended {
		t.Fatalf("got %v, %v", c.Action, err)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], ":2:") {
		t.Fatalf("warnings = %q, want one naming line 2", c.Warnings)
	}
	if got := readFile(t, path); !strings.HasPrefix(got, "a\n"+StartMarker+"\nb\n") || !strings.HasSuffix(got, testBlock) {
		t.Fatalf("file = %q", got)
	}
	if c, _ = ApplyBlock(path, testBlock); c.Action != Unchanged {
		t.Fatalf("second run: %v", c.Action)
	}
}

func TestApplyBlockTwoBlocksReplacesTheFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	old := StartMarker + "\nold\n" + EndMarker + "\n"
	writeTestFile(t, path, old+"mid\n"+old)

	c, _ := ApplyBlock(path, testBlock)
	if got := readFile(t, path); got != testBlock+"mid\n"+old {
		t.Fatalf("file = %q", got)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], ":5:") {
		t.Fatalf("warnings = %q, want one naming the second block", c.Warnings)
	}
}

func TestApplyBlockThroughASymlinkKeepsTheLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "bashrc")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, target, "x\n")
	link := filepath.Join(dir, ".bashrc")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := ApplyBlock(link, testBlock); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", info, err)
	}
	if got := readFile(t, target); !strings.Contains(got, StartMarker) || !strings.HasPrefix(got, "x\n") {
		t.Fatalf("target = %q", got)
	}
}

func TestApplyBlockKeepsFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	writeTestFile(t, path, "x\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyBlock(path, testBlock); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRemoveBlockLeavesTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	writeTestFile(t, path, "a\n\n"+testBlock+"b\n")

	c, err := RemoveBlock(path)
	if err != nil || c.Action != Removed {
		t.Fatalf("got %v, %v", c.Action, err)
	}
	if got := readFile(t, path); got != "a\nb\n" {
		t.Fatalf("file = %q", got)
	}
	if c, _ = RemoveBlock(path); c.Action != Absent {
		t.Fatalf("second run: %v", c.Action)
	}
	if c, _ = RemoveBlock(filepath.Join(t.TempDir(), "none")); c.Action != Absent {
		t.Fatalf("missing file: %v", c.Action)
	}
}

func TestApplyAndRemoveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf.d", "condatainer.fish")
	if c, err := ApplyFile(path, "one\n"); err != nil || c.Action != Created {
		t.Fatalf("create: %v, %v", c.Action, err)
	}
	if c, _ := ApplyFile(path, "one\n"); c.Action != Unchanged {
		t.Fatalf("same content: %v", c.Action)
	}
	if c, _ := ApplyFile(path, "two\n"); c.Action != Updated || readFile(t, path) != "two\n" {
		t.Fatalf("update: %v", c.Action)
	}
	if c, _ := RemoveFile(path); c.Action != Removed {
		t.Fatalf("remove: %v", c.Action)
	}
	if c, _ := RemoveFile(path); c.Action != Absent {
		t.Fatalf("remove again: %v", c.Action)
	}
}
