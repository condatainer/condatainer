package shell

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setHomes points the real home and the replacement home at fresh directories.
func setHomes(t *testing.T, override bool) (home, replacement string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CNT_REAL_HOME", "")
	t.Setenv("ZDOTDIR", "")
	if override {
		replacement = filepath.Join(t.TempDir(), "home")
	}
	oldContainer, oldOverride := insideContainer, homeOverride
	insideContainer = func() bool { return false }
	homeOverride = func() (string, error) { return replacement, nil }
	t.Cleanup(func() { insideContainer, homeOverride = oldContainer, oldOverride })
	return home, replacement
}

func TestInitWritesTheRealHomeThenTheReplacementHome(t *testing.T) {
	home, replacement := setHomes(t, true)

	res, err := Init(Options{Shell: Bash, Alias: "cnt", Exe: "/opt/cnt/bin/condatainer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 {
		t.Fatalf("changes = %v, want the two homes", res.Changes)
	}
	for _, path := range []string{filepath.Join(home, ".bashrc"), filepath.Join(replacement, ".bashrc")} {
		got := readFile(t, path)
		if !strings.Contains(got, "--alias cnt") || !strings.Contains(got, "__cnt_bin='/opt/cnt/bin'") {
			t.Errorf("%s = %q", path, got)
		}
	}

	if _, err := Remove([]Shell{Bash}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".bashrc"), filepath.Join(replacement, ".bashrc")} {
		if got := readFile(t, path); strings.Contains(got, StartMarker) {
			t.Errorf("%s still has the block: %q", path, got)
		}
	}
}

func TestInitWithTheReplacementHomeBeingTheRealHomeWritesOnce(t *testing.T) {
	home, _ := setHomes(t, false)
	homeOverride = func() (string, error) { return home, nil }

	res, err := Init(Options{Shell: Bash, Exe: "/opt/cnt/bin/condatainer"})
	if err != nil || len(res.Changes) != 1 {
		t.Fatalf("changes = %v, err = %v", res.Changes, err)
	}
}

func TestInitFishWritesAnOwnedFileAndRemoveDeletesIt(t *testing.T) {
	home, _ := setHomes(t, false)
	path := filepath.Join(home, ".config", "fish", "conf.d", "condatainer.fish")

	if _, err := Init(Options{Shell: Fish, Exe: "/opt/cnt/bin/condatainer"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "fish_add_path -g '/opt/cnt/bin'") {
		t.Fatalf("file = %q", got)
	}
	if _, err := Remove([]Shell{Fish}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}

func TestInitSetsUpBashWithAnyOtherShell(t *testing.T) {
	home, _ := setHomes(t, false)

	res, err := Init(Options{Shell: Zsh, Exe: "/opt/cnt/bin/condatainer"})
	if err != nil || len(res.Changes) != 2 {
		t.Fatalf("changes = %v, err = %v", res.Changes, err)
	}
	for _, name := range []string{".zshrc", ".bashrc"} {
		if got := readFile(t, filepath.Join(home, name)); !strings.Contains(got, StartMarker) {
			t.Errorf("%s = %q", name, got)
		}
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %q, want none for a bash nobody asked for", res.Warnings)
	}
}

func TestRemoveEveryShellLeavesNoSetup(t *testing.T) {
	home, _ := setHomes(t, false)
	for _, sh := range []Shell{Zsh, Fish} {
		if _, err := Init(Options{Shell: sh, Exe: "/opt/cnt/bin/condatainer"}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Remove(All)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, c := range res.Changes {
		if c.Action == Removed {
			removed++
		}
	}
	if removed != 3 {
		t.Errorf("removed %d files, want the zsh, bash and fish ones: %v", removed, res.Changes)
	}
	if got := readFile(t, filepath.Join(home, ".zshrc")); strings.Contains(got, StartMarker) {
		t.Errorf(".zshrc still has the block: %q", got)
	}
}

func TestInitZshUsesZDOTDIR(t *testing.T) {
	setHomes(t, false)
	zdir := t.TempDir()
	t.Setenv("ZDOTDIR", zdir)

	if _, err := Init(Options{Shell: Zsh, Exe: "/opt/cnt/bin/condatainer"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(zdir, ".zshrc")); !strings.Contains(got, StartMarker) {
		t.Fatalf("file = %q", got)
	}
}

func TestInitRefusesInsideAContainer(t *testing.T) {
	setHomes(t, false)
	insideContainer = func() bool { return true }

	if _, err := Init(Options{Shell: Bash, Exe: "/x/condatainer"}); !errors.Is(err, ErrInContainer) {
		t.Errorf("Init = %v, want ErrInContainer", err)
	}
	if _, err := Remove([]Shell{Bash}); !errors.Is(err, ErrInContainer) {
		t.Errorf("Remove = %v, want ErrInContainer", err)
	}
}

func TestInitRefusesABadAlias(t *testing.T) {
	setHomes(t, false)
	if _, err := Init(Options{Shell: Bash, Alias: "a b", Exe: "/x/condatainer"}); !errors.Is(err, ErrInvalidAlias) {
		t.Errorf("Init = %v, want ErrInvalidAlias", err)
	}
}

func TestInitBashWarnsWhenALoginShellSkipsTheRC(t *testing.T) {
	home, _ := setHomes(t, false)

	res, err := Init(Options{Shell: Bash, Exe: "/x/condatainer"})
	if err != nil || len(res.Warnings) != 1 {
		t.Fatalf("no login file: warnings = %q, err = %v", res.Warnings, err)
	}

	writeTestFile(t, filepath.Join(home, ".bash_profile"), "export A=1\n")
	if res, _ = Init(Options{Shell: Bash, Exe: "/x/condatainer"}); len(res.Warnings) != 1 {
		t.Fatalf("profile without .bashrc: warnings = %q", res.Warnings)
	}

	writeTestFile(t, filepath.Join(home, ".bash_profile"), "[ -f ~/.bashrc ] && . ~/.bashrc\n")
	if res, _ = Init(Options{Shell: Bash, Exe: "/x/condatainer"}); len(res.Warnings) != 0 {
		t.Fatalf("profile with .bashrc: warnings = %q", res.Warnings)
	}
}
