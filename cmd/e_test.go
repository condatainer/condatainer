package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/image"
)

// envDir is a directory holding env.img, and env.sqf when snapshot is set.
func envDir(t *testing.T, snapshot bool) (dir, img, sqf string) {
	t.Helper()
	t.Setenv("USER", "alice")
	dir = t.TempDir()
	img, sqf = filepath.Join(dir, "env.img"), filepath.Join(dir, "env.sqf")
	if err := os.WriteFile(img, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !snapshot {
		return dir, img, sqf
	}
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		t.Skip("mksquashfs not available")
	}
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("unsquashfs not available")
	}
	root := t.TempDir()
	rt := meta.Runtime{
		SchemaVersion: meta.SchemaVersion,
		Name:          meta.EnvName,
		Type:          artifact.TypeEnv,
		Platform:      meta.NativePlatform(),
		Prefix:        meta.EnvPrefix,
	}
	if err := meta.StageRuntime(filepath.Join(root, meta.DirName), rt); err != nil {
		t.Fatal(err)
	}
	pack := exec.Command("mksquashfs", root, sqf, "-no-progress", "-noappend", "-quiet", "-no-xattrs")
	if out, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("mksquashfs: %v\n%s", err, out)
	}
	return dir, img, sqf
}

func TestAutoloadEnv(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot bool
		hold     string // "", "writer" or "reader"
		readOnly bool
		want     func(img, sqf string) []string
	}{
		{"img alone", false, "", false, func(img, _ string) []string { return []string{img} }},
		{"img with its snapshot", true, "", false, func(img, sqf string) []string { return []string{img, sqf} }},
		{"read-only img with its snapshot", true, "", true, func(img, sqf string) []string { return []string{img, sqf} }},
		{"read-only img held by readers", true, "reader", true, func(img, sqf string) []string { return []string{img, sqf} }},
		{"img held by a writer loads nothing", true, "writer", false, func(_, _ string) []string { return nil }},
		{"img held by readers loads nothing", true, "reader", false, func(_, _ string) []string { return nil }},
		{"read-only img held by a writer loads nothing", true, "writer", true, func(_, _ string) []string { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, img, sqf := envDir(t, tc.snapshot)
			if tc.hold != "" {
				held, err := image.AcquireLock(img, tc.hold == "writer")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
			}
			got, announce := autoloadEnv(dir, tc.readOnly)
			if want := tc.want(img, sqf); !slices.Equal(got, want) {
				t.Errorf("autoloaded %v, want %v", got, want)
			}
			if announce == nil {
				t.Error("an autoload says what it did")
			}
		})
	}
}

func TestAutoloadEnvLoadsAnExt3WithItsSnapshot(t *testing.T) {
	dir, img, sqf := envDir(t, true)
	ext3 := filepath.Join(dir, "env.ext3")
	if err := os.Rename(img, ext3); err != nil {
		t.Fatal(err)
	}
	if got, _ := autoloadEnv(dir, false); !slices.Equal(got, []string{ext3, sqf}) {
		t.Errorf("autoloaded %v, want %v", got, []string{ext3, sqf})
	}
}

func TestAutoloadEnvLoadsASnapshotWithNoImg(t *testing.T) {
	dir, img, sqf := envDir(t, true)
	if err := os.Remove(img); err != nil {
		t.Fatal(err)
	}
	if got, _ := autoloadEnv(dir, false); !slices.Equal(got, []string{sqf}) {
		t.Errorf("autoloaded %v, want the snapshot alone", got)
	}
}

func TestUnavailableHint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hold     string
		snapshot bool
		readOnly bool
		want     string
	}{
		{"readers hold it: -r opens it", "reader", true, false, " (-r mounts it read-only)"},
		{"a writer holds it: name the snapshot", "writer", true, false, " (e env.sqf loads the snapshot read-only)"},
		{"-r cannot open it: name the snapshot", "writer", true, true, " (e env.sqf loads the snapshot read-only)"},
		{"a writer holds it and there is no snapshot", "writer", false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, img, sqf := envDir(t, tc.snapshot)
			held, err := image.AcquireLock(img, tc.hold == "writer")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !tc.snapshot {
				sqf = ""
			}
			if got := unavailableHint(img, sqf, tc.readOnly); got != tc.want {
				t.Errorf("hint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAutoloadEnvWithNothingToLoad(t *testing.T) {
	t.Setenv("USER", "alice")
	got, announce := autoloadEnv(t.TempDir(), false)
	if got != nil || announce != nil {
		t.Errorf("autoloaded %v from an empty directory", got)
	}
}
