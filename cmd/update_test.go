package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		v1, v2 string
		want   int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"1.2.3", "v1.2.3", 0},
		{"v1.2.3-alpha", "v1.2.3", -1},
		{"v1.2.3", "v1.2.3-alpha", 1},
		{"v1.2.3-alpha", "v1.2.3-beta", -1},
		{"v1.2.3+001", "v1.2.3+002", -1},
		{"v1.2.4", "v1.2.3+999", 1},
	}
	for _, c := range cases {
		got := compareVersions(c.v1, c.v2)
		if got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d; want %d", c.v1, c.v2, got, c.want)
		}
	}
}

// The replaced binary stays reachable under a hidden name until the next
// update, which replaces it.
func TestKeepPreviousHoldsTheReplacedBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "condatainer")
	prev := filepath.Join(dir, ".condatainer.prev")
	for _, content := range []string{"v1", "v2"} {
		if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		keepPrevious(exe)
		if err := os.Rename(exe, exe+".gone"); err != nil { // the update's rename drops this name
			t.Fatal(err)
		}
		if got, err := os.ReadFile(prev); err != nil || string(got) != content {
			t.Fatalf(".prev = %q, %v; want the replaced %q", got, err, content)
		}
	}
}
