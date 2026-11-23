package container

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
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
		Type:          artifact.TypeEnv,
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

// A backup keeps its .img and snapshot as a pair under the backup name, and the
// pair does not touch the live one.
func TestBackupPairIsSeparateFromTheLivePair(t *testing.T) {
	requireSquashfsTools(t)
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	for _, name := range []string{"env.sqf", "old.sqf"} {
		packRuntimeSqf(t, filepath.Join(dir, name), envRuntime())
	}
	for img, want := range map[string]string{
		"env-alice.img": "env.sqf",
		"old.ext3":      "old.sqf",
	} {
		if got := LookupSnapshot(filepath.Join(dir, img)).Path; got != filepath.Join(dir, want) {
			t.Errorf("%s pairs with %q, want %s", img, got, want)
		}
	}
}

// PairsWith is the name rule alone, so it needs no snapshot on disk. A backup
// name selects no live snapshot.
func TestPairsWithFollowsTheImageName(t *testing.T) {
	t.Setenv("USER", "alice")
	for name, want := range map[string]bool{
		"env.ext3":       true,
		"env-alice.img":  true,
		"env-backup.img": false,
		"other.img":      false,
	} {
		if got := PairsWith("/proj/"+name, "/proj/env.sqf"); got != want {
			t.Errorf("PairsWith(%s) = %v, want %v", name, got, want)
		}
	}
	if PairsWith("/proj/env.img", "/elsewhere/env.sqf") {
		t.Error("a snapshot in another directory pairs")
	}
}

// An env.ext3 pairs with env.sqf, and the snapshot is found with no writable
// overlay beside it.
func TestEnvOverlayAndSnapshotWithAnExt3(t *testing.T) {
	requireSquashfsTools(t)
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	sqf := filepath.Join(dir, "env.sqf")
	packRuntimeSqf(t, sqf, envRuntime())

	if got := FindEnvSnapshot(dir); got != sqf {
		t.Errorf("FindEnvSnapshot = %q, want %s", got, sqf)
	}
	if got := ResolveEnvOverlay("", dir); got != sqf {
		t.Errorf("with no writable overlay, ResolveEnvOverlay = %q, want the snapshot", got)
	}
	ext3 := filepath.Join(dir, "env.ext3")
	if err := os.WriteFile(ext3, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveEnvOverlay("", dir); got != ext3 {
		t.Errorf("ResolveEnvOverlay = %q, want %s", got, ext3)
	}
	if got := LookupSnapshot(ext3).Path; got != sqf {
		t.Errorf("env.ext3 pairs with %q, want %s", got, sqf)
	}
}

func TestNamesEnvironment(t *testing.T) {
	requireSquashfsTools(t)
	dir := t.TempDir()
	envSqf := filepath.Join(dir, "env.sqf")
	packRuntimeSqf(t, envSqf, envRuntime())
	appSqf := filepath.Join(dir, "tool.sqf")
	app := envRuntime()
	app.Type, app.Name, app.Prefix = artifact.TypeApp, "tool/1.0", "/cnt/tool/1.0"
	packRuntimeSqf(t, appSqf, app)

	for _, tc := range []struct {
		name     string
		overlays []string
		want     bool
	}{
		{"nothing", nil, false},
		{"an installed name", []string{"samtools/1.22"}, false},
		{"an app overlay", []string{appSqf}, false},
		{"a writable image", []string{filepath.Join(dir, "env.img")}, true},
		{"an environment snapshot", []string{envSqf}, true},
		{"a snapshot with a mode", []string{envSqf + ":ro"}, true},
		{"a snapshot among others", []string{"samtools/1.22", envSqf}, true},
	} {
		if got := NamesEnvironment(tc.overlays); got != tc.want {
			t.Errorf("%s: NamesEnvironment = %v, want %v", tc.name, got, tc.want)
		}
	}
}
