package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

// insideContainer and homeOverride are replaced by tests: config caches what it reads, and the suite runs wherever it runs.
var (
	insideContainer = config.IsInsideContainer
	homeOverride    = config.HomeOverrideDir
)

// Options says what Init writes.
type Options struct {
	// Shell is the shell to set up. Bash is set up with any other shell.
	Shell Shell
	Alias string
	// Exe is the running binary with symlinks resolved.
	Exe string
}

// Result is the changes made and the problems that did not stop the run.
type Result struct {
	Changes  []Change
	Warnings []string
}

// Init writes the setup for opts.Shell, and for bash, into the real home's rc file.
//   - With home_override set, the file of the same name under the replacement home is written next, since a container reads that one.
//   - A home_override that cannot be used is a warning, not an error.
func Init(opts Options) (Result, error) {
	if insideContainer() {
		return Result{}, ErrInContainer
	}
	if opts.Alias != "" && !ValidAlias(opts.Alias) {
		return Result{}, fmt.Errorf("%w %q: use letters, digits, '-' or '_' only", ErrInvalidAlias, opts.Alias)
	}
	h, res, err := findHomes()
	if err != nil {
		return Result{}, err
	}
	binDir := pathEntry(opts.Exe)
	shells, impliedBash := withBash([]Shell{opts.Shell})
	for _, sh := range shells {
		setup := Setup{Shell: sh, Alias: opts.Alias, BinDir: binDir}
		for _, path := range h.files(sh) {
			var change Change
			if sh == Fish {
				change, err = ApplyFile(path, setup.Block())
			} else {
				change, err = ApplyBlock(path, setup.Block())
			}
			if err != nil {
				return res, err
			}
			res.Changes = append(res.Changes, change)
			res.Warnings = append(res.Warnings, change.Warnings...)
		}
		if sh == Bash && !impliedBash {
			res.Warnings = append(res.Warnings, loginWarning(h.real)...)
		}
	}
	return res, nil
}

// withBash adds bash after shells that lack it, since a container starts bash unless told otherwise.
// implied reports that bash was added.
func withBash(shells []Shell) (out []Shell, implied bool) {
	out = append([]Shell(nil), shells...)
	if len(out) == 0 || slices.Contains(out, Bash) {
		return out, false
	}
	return append(out, Bash), true
}

// Remove takes the setup for each of shells out of the same files Init writes.
func Remove(shells []Shell) (Result, error) {
	if insideContainer() {
		return Result{}, ErrInContainer
	}
	h, res, err := findHomes()
	if err != nil {
		return Result{}, err
	}
	for _, sh := range shells {
		for _, path := range h.files(sh) {
			var change Change
			if sh == Fish {
				change, err = RemoveFile(path)
			} else {
				change, err = RemoveBlock(path)
			}
			if err != nil {
				return res, err
			}
			res.Changes = append(res.Changes, change)
			res.Warnings = append(res.Warnings, change.Warnings...)
		}
	}
	return res, nil
}

// homes are the directories whose rc files are written: the real home, and the replacement home when home_override is set.
type homes struct {
	real, replacement string
}

// files lists the rc files for sh: the real home's, then the replacement home's.
func (h homes) files(sh Shell) []string {
	files := []string{RCFile(sh, h.real, os.Getenv("ZDOTDIR"))}
	if h.replacement != "" {
		files = append(files, RCFile(sh, h.replacement, ""))
	}
	return files
}

func findHomes() (homes, Result, error) {
	var res Result
	real, err := utils.RealHome()
	if err != nil {
		return homes{}, res, fmt.Errorf("cannot find the home directory: %w", err)
	}
	h := homes{real: real}
	dir, err := homeOverride()
	switch {
	case err != nil:
		res.Warnings = append(res.Warnings, fmt.Sprintf("home_override skipped: %v", err))
	case dir != real:
		h.replacement = dir
	}
	return h, res, nil
}

// pathEntry returns the directory of exe for a PATH line, or "" when running condatainer on PATH already finds exe.
func pathEntry(exe string) string {
	if found, err := exec.LookPath("condatainer"); err == nil {
		a, errA := os.Stat(found)
		b, errB := os.Stat(exe)
		if errA == nil && errB == nil && os.SameFile(a, b) {
			return ""
		}
	}
	return filepath.Dir(exe)
}

// loginWarning says so when a login bash would not read the rc file.
//   - A login bash reads the first of .bash_profile, .bash_login and .profile that exists.
func loginWarning(home string) []string {
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		data, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), ".bashrc") {
			return nil
		}
		return []string{fmt.Sprintf("~/%s does not load ~/.bashrc, so a login bash skips the setup; add `[ -f ~/.bashrc ] && . ~/.bashrc` to it", name)}
	}
	return []string{"no ~/.bash_profile, ~/.bash_login or ~/.profile loads ~/.bashrc, so a login bash skips the setup; create ~/.bash_profile with `[ -f ~/.bashrc ] && . ~/.bashrc`"}
}
