package ext3

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestShutDownCleanlyReadsTheSuperblockState(t *testing.T) {
	if _, err := exec.LookPath("mke2fs"); err != nil {
		t.Skip("mke2fs not available")
	}
	path := filepath.Join(t.TempDir(), "env.img")
	if err := os.WriteFile(path, make([]byte, 8<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("mke2fs", "-q", "-t", "ext3", "-F", path).Run(); err != nil {
		t.Fatal(err)
	}
	if clean, err := ShutDownCleanly(path); err != nil || !clean {
		t.Fatalf("fresh image: clean=%v err=%v", clean, err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt([]byte{0, 0}, stateOffset); err != nil {
		t.Fatal(err)
	}
	if clean, err := ShutDownCleanly(path); err != nil || clean {
		t.Fatalf("image with the valid flag cleared: clean=%v err=%v", clean, err)
	}

	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(plain, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ShutDownCleanly(plain); err == nil {
		t.Fatal("a file that is not a filesystem was accepted")
	}
}
