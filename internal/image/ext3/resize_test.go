package ext3

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/image"
)

func TestResizeNeedsTheImageFreeAndReleasesItAfterwards(t *testing.T) {
	for _, tool := range []string{"mke2fs", "e2fsck", "resize2fs", "tune2fs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not available")
		}
	}
	path := filepath.Join(t.TempDir(), "env.img")
	if err := os.WriteFile(path, make([]byte, 16<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("mke2fs", "-q", "-t", "ext3", "-F", path).Run(); err != nil {
		t.Fatal(err)
	}

	held, err := image.AcquireLock(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := Resize(context.Background(), path, 32, true); !errors.Is(err, image.ErrInUse) {
		t.Fatalf("resize of an image in use: got %v, want ErrInUse", err)
	}
	if info, _ := os.Stat(path); info.Size() != 16<<20 {
		t.Fatalf("image changed while in use: %d bytes", info.Size())
	}
	held.Close()

	if err := Resize(context.Background(), path, 32, true); err != nil {
		t.Fatal(err)
	}
	stats, err := GetStats(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats.TotalBlocks * stats.BlockSize; got != 32<<20 {
		t.Fatalf("filesystem is %d bytes, want %d", got, 32<<20)
	}
	if err := image.CheckAvailable(path, true); err != nil {
		t.Fatalf("lock still held after resize: %v", err)
	}
}
