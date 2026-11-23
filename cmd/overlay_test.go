package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveOverlayPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "taken.img"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.img"), 0o755); err != nil {
		t.Fatal(err)
	}

	for label, tc := range map[string]struct {
		name, ext string
		want      string // "" when refused
	}{
		"an .img name":                       {filepath.Join(dir, "a.img"), ".img", filepath.Join(dir, "a.img")},
		"an .ext3 name":                      {filepath.Join(dir, "a.ext3"), ".img", filepath.Join(dir, "a.ext3")},
		"no extension takes the default":     {filepath.Join(dir, "a"), ".ext3", filepath.Join(dir, "a.ext3")},
		"a folder not there yet":             {filepath.Join(dir, "x", "y", "a"), ".img", filepath.Join(dir, "x", "y", "a.img")},
		"another extension":                  {filepath.Join(dir, "a.sqf"), ".img", ""},
		"a dotted name":                      {filepath.Join(dir, "a.b"), ".img", ""},
		"empty":                              {"", ".img", ""},
		"a trailing separator":               {dir + "/", ".img", ""},
		"an existing file":                   {filepath.Join(dir, "taken.img"), ".img", ""},
		"an existing file after the default": {filepath.Join(dir, "taken"), ".img", ""},
		"an existing directory":              {filepath.Join(dir, "folder.img"), ".img", ""},
	} {
		got, err := resolveOverlayPath(tc.name, tc.ext)
		if (err == nil) != (tc.want != "") || err == nil && got != tc.want {
			t.Errorf("%s: got %q, err %v; want %q", label, got, err, tc.want)
		}
	}

	// Resolving makes nothing: the folder is made only by the command that writes.
	if _, err := os.Stat(filepath.Join(dir, "x")); err == nil {
		t.Error("resolveOverlayPath created a folder")
	}
}

func TestMakeOverlayParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "dev.img")
	if err := makeOverlayParent(path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		t.Errorf("the folder was not made: %v", err)
	}
	if err := makeOverlayParent(path); err != nil {
		t.Errorf("a folder already there is an error: %v", err)
	}
}

func TestRefusePairing(t *testing.T) {
	t.Setenv("USER", "alice")
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "env.sqf")
	for name, refused := range map[string]bool{
		"env.img":        true,
		"env.ext3":       true,
		"env-alice.img":  true,
		"dev.img":        false,
		"env-backup.img": false,
	} {
		if err := refusePairing(filepath.Join(dir, name), snapshot); (err != nil) != refused {
			t.Errorf("%s: err = %v, refused = %v", name, err, refused)
		}
	}
	if err := refusePairing(filepath.Join(t.TempDir(), "env.img"), snapshot); err != nil {
		t.Errorf("another folder pairs: %v", err)
	}
}
