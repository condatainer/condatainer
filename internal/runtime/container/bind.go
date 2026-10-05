package container

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// BoundExecDir and BoundExecPath are where the running executable is bound
// inside a container, so a nested call has a stable path to reach it by.
//
// Not /usr/bin. A bind there puts a mount boundary inside the directory dpkg
// unpacks into most, and dpkg then reads the copied-up /usr as something other
// than a directory and refuses every package — "trying to overwrite '/usr',
// which is also in package login".
//
// Not under the metadata directory /.cnt either, which belongs to the image's own
// metadata. Its own directory is CondaTainer's, and no package writes there.
const (
	BoundExecDir  = "/.cnt_bin"
	BoundExecPath = BoundExecDir + "/condatainer"
)

// tmpDirEnvVars are the temp directory environment variables read by name, in
// priority order: CNT_TMPDIR is condatainer's own staging root, the rest are what
// the C++ standard library's temp_directory_path() consults. The scheduler's
// node-local tmp is not here — its variable depends on the detected scheduler,
// so TmpDirBinds resolves it through scheduler.ActiveTmpDir.
var tmpDirEnvVars = []string{"CNT_TMPDIR", "TMPDIR", "TMP", "TEMP", "TEMPDIR"}

// TmpDirBinds returns every host temp directory that exists, so a tool inside the
// container can follow whichever variable it reads. Apptainer binds only /tmp by
// default, and an unbound variable names a host path that is not there — which is
// what micromamba dies on when the site profile exports one of these.
func TmpDirBinds() []string {
	dirs := make([]string, 0, len(tmpDirEnvVars)+1)
	for _, env := range tmpDirEnvVars {
		dirs = append(dirs, os.Getenv(env))
	}
	// Scheduler-assigned node-local tmp (fast SSD scratch): the variable it comes
	// from is whatever the detected scheduler names.
	dirs = append(dirs, scheduler.ActiveTmpDir())

	binds := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		binds = append(binds, dir)
	}
	return binds
}

// DeduplicateBindPaths resolves host paths, collapses repeats and drops child paths a parent already covers.
//   - Formats: "/path", "/path:/container", "/path:/container:opts".
//   - A bind is its host and container path together. When one repeats, the later wins, so an explicit remap or ro replaces an automatic bind.
//   - A plain child is dropped only under a parent bound at its own path with no options.
func DeduplicateBindPaths(paths []string) []string {
	type bind struct{ host, dest, opts string }
	var binds []bind
	index := map[[2]string]int{}
	for _, raw := range paths {
		if raw == "" {
			continue
		}
		parts := strings.SplitN(raw, ":", 3)
		host, err := filepath.Abs(parts[0])
		if err != nil {
			host = parts[0]
		}
		if real, err := filepath.EvalSymlinks(host); err == nil {
			host = real
		}
		b := bind{host: host, dest: host}
		if len(parts) >= 2 && parts[1] != "" {
			b.dest = parts[1]
		}
		if len(parts) == 3 {
			b.opts = parts[2]
		}
		key := [2]string{b.host, b.dest}
		if i, ok := index[key]; ok {
			binds[i] = b
			continue
		}
		index[key] = len(binds)
		binds = append(binds, b)
	}

	plain := func(b bind) bool { return b.dest == b.host && b.opts == "" }
	out := make([]string, 0, len(binds))
	for _, b := range binds {
		if plain(b) {
			covered := false
			for _, p := range binds {
				if p.host == b.host || !plain(p) {
					continue
				}
				if rel, err := filepath.Rel(p.host, b.host); err == nil && !strings.HasPrefix(rel, "..") {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			out = append(out, b.host)
			continue
		}
		spec := b.host + ":" + b.dest
		if b.opts != "" {
			spec += ":" + b.opts
		}
		out = append(out, spec)
	}
	return out
}

// BindPaths collects all directories suitable for --bind flags.
// Deduplication and child path filtering is handled by DeduplicateBindPaths().
func BindPaths(paths ...string) []string {
	bindPaths := make([]string, 0, len(paths)+10)

	// Add provided paths
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		bindPaths = append(bindPaths, path)
	}

	// Add current working directory
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		bindPaths = append(bindPaths, cwd)
	}

	// Add $SCRATCH if set
	if scratch := os.Getenv("SCRATCH"); scratch != "" {
		bindPaths = append(bindPaths, scratch)
	}

	// Add every temp directory the host advertises.
	bindPaths = append(bindPaths, TmpDirBinds()...)

	// Collect all base directories
	baseDirs := []string{}

	// Extra root dir (CNT_EXTRA_ROOT, group/lab layer)
	if dir := config.GetExtraRootDir(); dir != "" {
		baseDirs = append(baseDirs, dir)
	}

	// Root, Scratch, User directories
	if dir := config.GetRootDir(); dir != "" {
		baseDirs = append(baseDirs, dir)
	}
	if dir := config.GetScratchDataDir(); dir != "" {
		baseDirs = append(baseDirs, dir)
	}
	if dir := config.GetUserDataDir(); dir != "" {
		baseDirs = append(baseDirs, dir)
	}

	// Add base directories
	for _, dir := range baseDirs {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		bindPaths = append(bindPaths, dir)
	}

	// Bind condatainer executable for nested calls.
	// Always bind the actual exe regardless of install location.
	if execPath, err := os.Executable(); err == nil && execPath != "" {
		bindPaths = append(bindPaths, execPath+":"+BoundExecPath+":ro")
	}

	bindPaths = append(bindPaths, realHomeBinds()...)

	return bindPaths
}

// realHomeBinds binds the real home read-only at its own path when home_override replaced HOME, so symlinks into it resolve and a nested condatainer finds the user's config through CNT_REAL_HOME.
//   - The config directory is bound alone when it lies outside the real home.
//   - A directory that contains or sits inside the replacement HOME is skipped.
func realHomeBinds() []string {
	if os.Getenv(utils.EnvRealHome) == "" {
		return nil
	}
	replaced := os.Getenv("HOME")
	var dirs []string
	home, err := utils.RealHome()
	if err == nil && utils.DirExists(home) && !pathsOverlap(home, replaced) {
		dirs = append(dirs, home)
	}
	cfg := config.GetUserConfigDir()
	if utils.DirExists(cfg) && !pathsOverlap(cfg, replaced) && !(len(dirs) > 0 && pathsOverlap(home, cfg)) {
		dirs = append(dirs, cfg)
	}
	binds := make([]string, len(dirs))
	for i, d := range dirs {
		binds[i] = d + ":" + d + ":ro"
	}
	return binds
}

// pathsOverlap reports whether a and b are the same path or one is inside the other.
func pathsOverlap(a, b string) bool {
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
	}
	return inside(a, b) || inside(b, a)
}

// replacedHomeFlags makes the replaced HOME the container's home.
//   - Apptainer's default home is the passwd home, not $HOME, so the replacement needs --home.
//   - Nothing is added when HOME was not replaced, or when userFlags already set a home.
func replacedHomeFlags(userFlags []string) []string {
	home := os.Getenv("HOME")
	if real, err := utils.RealHome(); os.Getenv(utils.EnvRealHome) == "" || err != nil || home == "" || home == real {
		return nil
	}
	for _, f := range userFlags {
		if f == "-H" || f == "--home" || strings.HasPrefix(f, "-H=") || strings.HasPrefix(f, "--home=") {
			return nil
		}
	}
	return []string{"--home", home}
}
