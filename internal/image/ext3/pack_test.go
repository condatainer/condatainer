package ext3

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func newStage(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("mke2fs"); err != nil {
		t.Skip("mke2fs not available")
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		t.Skip("debugfs not available")
	}
	stage := t.TempDir()
	env := filepath.Join(stage, "upper", "cnt_env")
	for _, dir := range []string{env, filepath.Join(stage, "work", "work")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a", opaqueMarker} {
		if err := os.WriteFile(filepath.Join(env, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stage
}

func packOpts(path string, sizeMB int) *CreateOptions {
	return &CreateOptions{Path: path, SizeMB: sizeMB, UID: os.Getuid(), GID: os.Getgid(),
		Profile: ProfileSmall, Sparse: true, Quiet: true}
}

func TestPackWritesImageAndLeavesNoPartial(t *testing.T) {
	stage := newStage(t)
	dest := filepath.Join(t.TempDir(), "env.img")
	if err := Pack(context.Background(), stage, packOpts(dest, 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("image missing: %v", err)
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "upper", "cnt_env", opaqueMarker)); !os.IsNotExist(err) {
		t.Fatalf("opaque marker not dropped: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "work", "work")); !os.IsNotExist(err) {
		t.Fatalf("work dir not emptied: %v", err)
	}
}

func TestPackRefusesUndersizedImage(t *testing.T) {
	stage := newStage(t)
	big := filepath.Join(stage, "upper", "cnt_env", "big")
	if err := os.WriteFile(big, make([]byte, 4<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "env.img")
	err := Pack(context.Background(), stage, packOpts(dest, 1))
	if !errors.Is(err, ErrTooSmall) {
		t.Fatalf("got %v, want ErrTooSmall", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("image written despite refusal: %v", statErr)
	}
}

func TestPackRefusesWhileAnotherCreateHoldsThePartial(t *testing.T) {
	stage := newStage(t)
	dest := filepath.Join(t.TempDir(), "env.img")
	held, err := lockPartial(context.Background(), dest+".partial")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := Pack(context.Background(), stage, packOpts(dest, 64)); !errors.Is(err, ErrCreateInProgress) {
		t.Fatalf("got %v, want ErrCreateInProgress", err)
	}
}

func TestPackReusesALeftoverPartial(t *testing.T) {
	stage := newStage(t)
	dest := filepath.Join(t.TempDir(), "env.img")
	if err := os.WriteFile(dest+".partial", []byte("half an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Pack(context.Background(), stage, packOpts(dest, 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: %v", err)
	}
}

func allocatedBytes(t *testing.T, path string) int64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatal(err)
	}
	return st.Blocks * 512
}

func TestNonSparseImageStaysAllocatedAfterFormatting(t *testing.T) {
	stage := newStage(t)
	probe, err := os.Create(filepath.Join(t.TempDir(), "probe"))
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	canReserve := syscall.Fallocate(int(probe.Fd()), 0, 0, 1<<20) == nil

	// A filesystem without fallocate gets zeros instead.
	for name, hook := range map[string]func(int, uint32, int64, int64) error{
		"fallocate": fallocate,
		"zeros":     func(int, uint32, int64, int64) error { return syscall.EOPNOTSUPP },
	} {
		if name == "fallocate" && !canReserve {
			continue
		}
		old := fallocate
		fallocate = hook
		const sizeMB = 64
		dir := t.TempDir()
		for kind, create := range map[string]func(*CreateOptions) error{
			"pack":   func(o *CreateOptions) error { return Pack(context.Background(), stage, o) },
			"direct": func(o *CreateOptions) error { return CreateDirectly(context.Background(), o) },
		} {
			opts := packOpts(filepath.Join(dir, kind+".img"), sizeMB)
			opts.Sparse = false
			if err := create(opts); err != nil {
				t.Fatalf("%s/%s: %v", name, kind, err)
			}
			if got := allocatedBytes(t, opts.Path); got < sizeMB<<20 {
				t.Errorf("%s/%s: %d bytes allocated, want %d", name, kind, got, sizeMB<<20)
			}
		}
		fallocate = old
	}
}

func TestCreateFailsAndLeavesNothingWhenSpaceCannotBeReserved(t *testing.T) {
	if _, err := exec.LookPath("mke2fs"); err != nil {
		t.Skip("mke2fs not available")
	}
	old := fallocate
	fallocate = func(int, uint32, int64, int64) error { return syscall.ENOSPC }
	defer func() { fallocate = old }()

	opts := packOpts(filepath.Join(t.TempDir(), "env.img"), 64)
	opts.Sparse = false
	if err := CreateDirectly(context.Background(), opts); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("got %v, want ENOSPC", err)
	}
	if _, err := os.Stat(opts.Path); !os.IsNotExist(err) {
		t.Fatalf("image left behind: %v", err)
	}
}
