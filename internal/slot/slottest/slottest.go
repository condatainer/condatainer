// Package slottest builds the small overlays that slot tests bind.
package slottest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// Image packs a data overlay named name into dir as a .sqf, with a runtime document that exports env and the files under its prefix.
//   - The test is skipped when the host cannot build or read a .sqf.
//   - The file is named as an installed overlay is, with the name's slashes written as --.
func Image(t *testing.T, dir, name string, env []meta.EnvVar, files ...string) string {
	t.Helper()
	return build(t, dir, name, artifact.TypeData, env, files)
}

// App is Image for an app overlay, which a bare or partial name can complete to.
func App(t *testing.T, dir, name string, env []meta.EnvVar, files ...string) string {
	t.Helper()
	return build(t, dir, name, artifact.TypeApp, env, files)
}

func build(t *testing.T, dir, name string, typ artifact.Type, env []meta.EnvVar, files []string) string {
	t.Helper()
	for _, bin := range []string{"mksquashfs", "unsquashfs"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	root := t.TempDir()
	prefix := "/cnt/" + name
	rt := meta.Runtime{
		SchemaVersion: meta.SchemaVersion,
		Name:          name,
		Type:          typ,
		Platform:      meta.Platform{OS: "linux", Arch: "amd64"},
		Prefix:        prefix,
		Env:           env,
	}
	if err := meta.StageRuntime(filepath.Join(root, meta.DirName), rt); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(root, prefix, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, fileName(name)+".sqf")
	if o, err := exec.Command("mksquashfs", root, out, "-no-progress", "-noappend", "-quiet", "-no-xattrs").CombinedOutput(); err != nil {
		t.Fatalf("mksquashfs: %v\n%s", err, o)
	}
	return out
}

func fileName(name string) string {
	out := make([]byte, 0, len(name)+8)
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			out = append(out, '-', '-')
		} else {
			out = append(out, name[i])
		}
	}
	return string(out)
}
