package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// The destination is read as every overlay path is, and refused where it would
// pair with the snapshot.
func TestResolveUnfreezeTarget(t *testing.T) {
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	artifact := filepath.Join(dir, "env.sqf")
	if err := os.WriteFile(artifact, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for label, tc := range map[string]struct {
		dest string
		want string // "" when refused
	}{
		"a path":                   {filepath.Join(dir, "dev.img"), filepath.Join(dir, "dev.img")},
		"a .ext3 path":             {filepath.Join(dir, "dev.ext3"), filepath.Join(dir, "dev.ext3")},
		"a name with no extension": {filepath.Join(dir, "work", "dev"), filepath.Join(dir, "work", "dev.img")},
		"a folder not there yet":   {filepath.Join(dir, "missing", "dev.img"), filepath.Join(dir, "missing", "dev.img")},
		"the snapshot's pair name": {filepath.Join(dir, "env.img"), ""},
		"the pair name as .ext3":   {filepath.Join(dir, "env.ext3"), ""},
		"the user's pair name":     {filepath.Join(dir, "env-alice.img"), ""},
		"no destination":           {"", ""},
		"a .sqf":                   {filepath.Join(dir, "dev.sqf"), ""},
	} {
		got, err := resolveUnfreezeTarget(artifact, tc.dest)
		if (err == nil) != (tc.want != "") || err == nil && got != tc.want {
			t.Errorf("%s: got %q, err %v; want %q", label, got, err, tc.want)
		}
	}
}
