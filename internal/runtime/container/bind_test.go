package container

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A bind is its host and container path together: a repeat is replaced by the
// later one, a remap sits beside the plain bind, and only a plain parent covers
// a plain child.
func TestDeduplicateBindPaths(t *testing.T) {
	x, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	y := filepath.Join(x, "y")
	if err := os.Mkdir(y, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"a later ro replaces the plain bind", []string{x, x + ":" + x + ":ro"}, []string{x + ":" + x + ":ro"}},
		{"a remap keeps the plain bind", []string{x, x + ":/work"}, []string{x, x + ":/work"}},
		{"a plain parent covers a plain child", []string{x, y}, []string{x}},
		{"a remapped parent does not", []string{x + ":/work", y}, []string{x + ":/work", y}},
		{"a read-only parent does not", []string{x + ":" + x + ":ro", y}, []string{x + ":" + x + ":ro", y}},
	} {
		if got := DeduplicateBindPaths(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("%s: DeduplicateBindPaths(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}
