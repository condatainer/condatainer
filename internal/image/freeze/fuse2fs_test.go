package freeze

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeTool writes an executable shell script that prints help when asked for it.
func fakeTool(t *testing.T, help string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fuse2fs")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'EOF'\n"+help+"\nEOF\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The mount asks fuse2fs to check permissions as root only when its help says it can.
func TestFuse2fsMountAddsFakerootOnlyWhereItIsSupported(t *testing.T) {
	with := fakeTool(t, "    -o fakeroot            pretend to be root for permission checks")
	without := fakeTool(t, "    -o minixdf             minix-style df")

	got := fuse2fsMount(with, "/i.img", "/m")
	if want := []string{"-o", "ro", "-o", "fakeroot", "/i.img"}; !slices.Equal(got.Args, want) || got.Bin != with || got.Path != "/m" {
		t.Errorf("mount = %+v, want args %v", got, want)
	}
	if got := fuse2fsMount(without, "/i.img", "/m"); !slices.Equal(got.Args, []string{"-o", "ro", "/i.img"}) {
		t.Errorf("args = %v, want no fakeroot for a fuse2fs without it", got.Args)
	}
}
