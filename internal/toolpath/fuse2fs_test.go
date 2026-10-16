package toolpath

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeFuse2fs(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fuse2fs")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFuse2fsProblem(t *testing.T) {
	cachePath = func() string { return "" }
	defer func() { cachePath = defaultCachePath }()
	defer func(f func(string) bool) { linksLibfuse3 = f }(linksLibfuse3)

	cases := []struct {
		name    string
		output  string
		libfuse bool
		bad     bool
	}{
		{"old on libfuse 3", "fuse2fs 1.47.1 (20-May-2024)", true, true},
		{"old on libfuse 2", "fuse2fs 1.47.0 (5-Feb-2023)", false, false},
		{"fixed on libfuse 3", "fuse2fs 1.47.3 (8-Jul-2025)", true, false},
		{"newer on libfuse 3", "fuse2fs 1.47.4 (6-Mar-2026)", true, false},
		{"version not readable", "usage: fuse2fs", true, false},
	}
	for _, c := range cases {
		linksLibfuse3 = func(string) bool { return c.libfuse }
		got := Fuse2fsProblem(fakeFuse2fs(t, c.output))
		if (got != "") != c.bad {
			t.Errorf("%s: Fuse2fsProblem = %q, want a problem: %v", c.name, got, c.bad)
		}
	}
}
