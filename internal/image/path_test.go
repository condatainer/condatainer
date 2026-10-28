package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// stageRoot makes a directory holding cnt_env, usr and a hidden .cnt.
func stageRoot(t *testing.T, top string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"cnt_env/conda-meta", "usr", ".cnt"} {
		if err := os.MkdirAll(filepath.Join(root, top, dir), 0o775); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TopLevel lists the entries at the root of a payload: the archive root of a
// .sqf, and upper/ of an .img.
func TestTopLevelListsTheRootOfEachFormat(t *testing.T) {
	want := []string{".cnt", "cnt_env", "usr"}

	t.Run("squashfs", func(t *testing.T) {
		if _, err := exec.LookPath("mksquashfs"); err != nil {
			t.Skip("mksquashfs not available")
		}
		if _, err := exec.LookPath("unsquashfs"); err != nil {
			t.Skip("unsquashfs not available")
		}
		out := filepath.Join(t.TempDir(), "env.sqf")
		cmd := exec.Command("mksquashfs", stageRoot(t, ""), out, "-no-progress", "-noappend", "-quiet", "-no-xattrs")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("mksquashfs: %v\n%s", err, output)
		}
		got, err := TopLevel(out)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("TopLevel = %v, %v; want %v", got, err, want)
		}
	})

	t.Run("ext3", func(t *testing.T) {
		for _, name := range []string{"mke2fs", "debugfs"} {
			if _, err := exec.LookPath(name); err != nil {
				t.Skip(name + " not available")
			}
		}
		out := filepath.Join(t.TempDir(), "env.img")
		cmd := exec.Command("mke2fs", "-q", "-t", "ext3", "-d", stageRoot(t, "upper"), out, "8M")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("mke2fs: %v\n%s", err, output)
		}
		got, err := TopLevel(out)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("TopLevel = %v, %v; want %v", got, err, want)
		}
	})

	if _, err := TopLevel("env.sif"); err == nil {
		t.Error("a format with no listing was accepted")
	}
}
