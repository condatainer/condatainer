package container

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/catalog"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

func requireSquashfsTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"mksquashfs", "unsquashfs"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
}

// packRuntimeSqf builds a .sqf at path whose only content is a runtime.json
// document, the way `overlay freeze` stages one.
func packRuntimeSqf(t *testing.T, path string, rt meta.Runtime) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, meta.DirName)
	if err := meta.StageRuntime(dir, rt); err != nil {
		t.Fatalf("StageRuntime: %v", err)
	}
	cmd := exec.Command("mksquashfs", root, path, "-no-progress", "-noappend", "-quiet", "-no-xattrs")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mksquashfs: %v\n%s", err, output)
	}
}

func envRuntime() meta.Runtime {
	return meta.Runtime{
		SchemaVersion: meta.SchemaVersion,
		Name:          meta.EnvName,
		Type:          catalog.TypeEnv,
		Platform:      meta.NativePlatform(),
		Prefix:        meta.EnvPrefix,
	}
}

func TestSnapshotStemStripsUserSuffix(t *testing.T) {
	t.Setenv("USER", "alice")
	if got := snapshotStem("/proj/env-alice.img"); got != "env" {
		t.Fatalf("stem = %q, want env", got)
	}
	if got := snapshotStem("/proj/env.img"); got != "env" {
		t.Fatalf("stem = %q, want env", got)
	}
	// A name that merely contains, but does not end with, "-<user>" keeps its
	// own trailing segment.
	if got := snapshotStem("/proj/env-alice-2.img"); got != "env-alice-2" {
		t.Fatalf("stem = %q, want env-alice-2", got)
	}
}

// Every .img of a name shares one snapshot.
func TestSnapshotPathIsSharedByEveryImageOfAName(t *testing.T) {
	t.Setenv("USER", "alice")
	for _, img := range []string{"/proj/env-alice.img", "/proj/env.img"} {
		if got := SnapshotPath(img); got != "/proj/env.sqf" {
			t.Errorf("SnapshotPath(%s) = %s, want /proj/env.sqf", img, got)
		}
	}
}

func TestLookupSnapshotFindsTheSharedSnapshot(t *testing.T) {
	requireSquashfsTools(t)
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	packRuntimeSqf(t, filepath.Join(dir, "env.sqf"), envRuntime())

	got := LookupSnapshot(filepath.Join(dir, "env-alice.img"))
	if got.Path != filepath.Join(dir, "env.sqf") {
		t.Fatalf("lookup = %+v, want the shared snapshot", got)
	}
}

func TestLookupSnapshotNoneFound(t *testing.T) {
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	got := LookupSnapshot(filepath.Join(dir, "env-alice.img"))
	if got.Path != "" || got.Blocked != "" {
		t.Fatalf("lookup = %+v, want nothing found", got)
	}
}

// A slot occupied by something that isn't an env-typed artifact is Blocked,
// not paired.
func TestLookupSnapshotBlockedByNonEnvFile(t *testing.T) {
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	notASnapshot := filepath.Join(dir, "env.sqf")
	if err := os.WriteFile(notASnapshot, []byte("not a squashfs image"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := LookupSnapshot(filepath.Join(dir, "env-alice.img"))
	if got.Path != "" || got.Blocked != notASnapshot {
		t.Fatalf("lookup = %+v, want Blocked on %s", got, notASnapshot)
	}
}
