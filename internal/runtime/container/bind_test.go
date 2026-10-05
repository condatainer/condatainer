package container

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
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

func TestRealHomeBinds(t *testing.T) {
	realHome := t.TempDir()
	cfgDir := filepath.Join(realHome, ".config", "condatainer")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")

	t.Setenv("HOME", realHome)
	t.Setenv(utils.EnvRealHome, "")
	if got := realHomeBinds(); len(got) != 0 {
		t.Errorf("binds with HOME not replaced: %v", got)
	}

	// The same path in both variables, as when home_override names the real home itself.
	t.Setenv(utils.EnvRealHome, realHome)
	if got := realHomeBinds(); len(got) != 0 {
		t.Errorf("binds with HOME equal to CNT_REAL_HOME: %v", got)
	}

	t.Setenv("HOME", t.TempDir())
	t.Setenv(utils.EnvRealHome, realHome)
	if got, want := realHomeBinds(), []string{realHome + ":" + realHome + ":ro"}; !slices.Equal(got, want) {
		t.Errorf("binds = %v, want %v", got, want)
	}

	// A replacement inside the real home leaves the home unbound; the config stays reachable.
	inside := filepath.Join(realHome, "scratch-home")
	t.Setenv("HOME", inside)
	if got, want := realHomeBinds(), []string{cfgDir + ":" + cfgDir + ":ro"}; !slices.Equal(got, want) {
		t.Errorf("binds with the replacement inside the real home = %v, want %v", got, want)
	}
}

func TestReplacedHomeFlags(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv(utils.EnvRealHome, "")
	if got := replacedHomeFlags(nil); len(got) != 0 {
		t.Errorf("flags with HOME not replaced: %v", got)
	}
	t.Setenv(utils.EnvRealHome, "/home/u")
	if got := replacedHomeFlags(nil); len(got) != 0 {
		t.Errorf("flags with HOME equal to CNT_REAL_HOME: %v", got)
	}
	t.Setenv("HOME", "/scratch/u/home")
	if got, want := replacedHomeFlags(nil), []string{"--home", "/scratch/u/home"}; !slices.Equal(got, want) {
		t.Errorf("flags = %v, want %v", got, want)
	}
	if got := replacedHomeFlags([]string{"--home", "/x"}); len(got) != 0 {
		t.Errorf("flags beside a user --home: %v", got)
	}
}
