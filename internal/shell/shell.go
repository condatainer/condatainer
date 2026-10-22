// Package shell writes and removes the shell setup in a user's rc files.
package shell

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Shell is a shell the setup is written for.
type Shell string

const (
	Bash Shell = "bash"
	Zsh  Shell = "zsh"
	Fish Shell = "fish"
)

var (
	// ErrUnsupportedShell is a shell name other than bash, zsh or fish.
	ErrUnsupportedShell = errors.New("unsupported shell")
	// ErrInvalidAlias is an alias that is not a plain shell word.
	ErrInvalidAlias = errors.New("invalid alias")
	// ErrInContainer is a setup command run inside a container.
	ErrInContainer = errors.New("run this on the host, not inside a container")
	// ErrNotWritable is an rc file that cannot be written.
	ErrNotWritable = errors.New("cannot write the shell file")
)

const (
	// StartMarker and EndMarker delimit the block in a bash or zsh rc file.
	StartMarker = "# >>> CONDATAINER >>>"
	EndMarker   = "# <<< CONDATAINER <<<"

	managedNote = "# Managed by `condatainer shell init`. Edits here are overwritten."
)

// aliasPattern is a plain shell word: the alias is interpolated into the rc file.
var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Parse returns the shell named by name.
func Parse(name string) (Shell, error) {
	switch Shell(name) {
	case Bash, Zsh, Fish:
		return Shell(name), nil
	}
	return "", fmt.Errorf("%w %q: use bash, zsh, or fish", ErrUnsupportedShell, name)
}

// All lists every supported shell.
var All = []Shell{Bash, Zsh, Fish}

// ValidAlias reports whether name can be an alias.
func ValidAlias(name string) bool { return aliasPattern.MatchString(name) }

// Setup is what a shell's rc file loads.
type Setup struct {
	Shell Shell
	// Alias is a short name registered next to condatainer; empty for none.
	Alias string
	// BinDir is put first on PATH; empty for no PATH line.
	BinDir string
}

// Block returns the setup text: the whole fish file, or the marked block for bash and zsh.
//   - The PATH line comes first, so the lines after it find condatainer.
//   - The completion and hook lines run only when condatainer is found.
func (s Setup) Block() string {
	var b strings.Builder
	if s.Shell == Fish {
		b.WriteString(managedNote + "\n")
		if s.BinDir != "" {
			fmt.Fprintf(&b, "fish_add_path -g %s\n", quote(s.BinDir))
		}
		b.WriteString("if command -q condatainer\n")
		fmt.Fprintf(&b, "    condatainer shell completion fish%s | source\n", s.aliasFlag())
		b.WriteString("    condatainer env hook --shell fish | source\n")
		b.WriteString("end\n")
		return b.String()
	}
	b.WriteString(StartMarker + "\n" + managedNote + "\n")
	if s.BinDir != "" {
		fmt.Fprintf(&b, "__cnt_bin=%s\n", quote(s.BinDir))
		b.WriteString("if [[ \":$PATH:\" != *\":$__cnt_bin:\"* ]]; then\n")
		b.WriteString("    export PATH=\"$__cnt_bin:$PATH\"\n")
		b.WriteString("fi\n")
		b.WriteString("unset __cnt_bin\n")
	}
	b.WriteString("if command -v condatainer &> /dev/null; then\n")
	fmt.Fprintf(&b, "    source <(condatainer shell completion %s%s)\n", s.Shell, s.aliasFlag())
	fmt.Fprintf(&b, "    source <(condatainer env hook --shell %s)\n", s.Shell)
	b.WriteString("fi\n")
	b.WriteString(EndMarker + "\n")
	return b.String()
}

func (s Setup) aliasFlag() string {
	if s.Alias == "" {
		return ""
	}
	return " --alias " + s.Alias
}

// quote renders a path as a single-quoted shell word, valid in bash, zsh and fish.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RCFile returns the file that holds the setup for sh under home.
//   - zshDir is ZDOTDIR; when set, zsh reads its .zshrc there instead of home.
func RCFile(sh Shell, home, zshDir string) string {
	switch sh {
	case Zsh:
		if zshDir != "" {
			return filepath.Join(zshDir, ".zshrc")
		}
		return filepath.Join(home, ".zshrc")
	case Fish:
		return filepath.Join(home, ".config", "fish", "conf.d", "condatainer.fish")
	}
	return filepath.Join(home, ".bashrc")
}
