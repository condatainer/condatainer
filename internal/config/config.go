// Package config holds the layered configuration, the global settings singleton,
// and the search order for data directories.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/utils"
)

// ExitCodeJobsSubmitted is returned by condatainer commands when build jobs are
// submitted to the scheduler rather than run immediately. Callers (e.g. helper
// overlay checks) can detect this exit code to show a more helpful message.
const ExitCodeJobsSubmitted = 3

// Version is a variable so tagged release builds can set it with -ldflags -X
// without modifying tracked source before compilation.
var Version = "0.1.0"

const GitHubRepo = "condatainer/condatainer"

// Config holds the settings that are runtime facts rather than config keys.
type Config struct {
	Debug   bool
	Version string

	// Directory paths
	ProgramDir string

	// Recipe collections, in order. Earlier entries shadow later ones.
	Sources []catalog.Spec

	// SkipPrebuilt builds from the recipe rather than pulling a prebuilt artifact.
	// It is set per run by create --no-prebuilt and never read from a file.
	SkipPrebuilt bool
}

// DefaultNcpus is the build.ncpus default, which callers that cannot import build fall back to.
const DefaultNcpus = 4

// Global holds the singleton configuration instance
var Global Config

func LoadDefaults(executablePath string) {
	programDir := filepath.Dir(executablePath)
	if absProgDir, err := filepath.Abs(programDir); err == nil {
		programDir = absProgDir
	}

	Global = Config{
		Version:    Version,
		ProgramDir: programDir,
	}
}

// IsInsideContainer checks if we're currently running inside a container
func IsInsideContainer() bool {
	// Check for IN_CONDATAINER environment variable (our own containers)
	if os.Getenv("IN_CONDATAINER") != "" {
		return true
	}

	// Check for standard Apptainer/Singularity environment variables
	if os.Getenv("APPTAINER_NAME") != "" || os.Getenv("SINGULARITY_NAME") != "" {
		return true
	}

	// Check for Apptainer/Singularity filesystem markers
	if _, err := os.Stat("/.singularity.d"); err == nil {
		return true
	}
	if _, err := os.Stat("/.apptainer.d"); err == nil {
		return true
	}

	return false
}

// BaseImageFileName returns the expected filename for the configured base, e.g.
// "ubuntu24" → "ubuntu24--base.sqf" — the flat name any artifact of that name
// gets. Empty when no base is configured, so callers do not go looking for a
// file called ".sqf".
func BaseImageFileName() string {
	name := BaseRecipeName()
	if name == "" {
		return ""
	}
	return strings.ReplaceAll(name, "/", "--") + ".sqf"
}

// GetBaseImage returns the installed base image, searching every image directory.
//   - It only returns a file that exists.
//   - Where a base could be written is a different question.
func GetBaseImage() (string, error) {
	if found := FindBaseImage(); found != "" {
		return found, nil
	}
	name := BaseRecipeName()
	if name == "" {
		return "", fmt.Errorf("no default distro configured: set `default_distro`, or configure a source declaring default_distro")
	}
	return "", fmt.Errorf("base image %s is not installed (searched %s)",
		name, strings.Join(GetImageSearchPaths(), ", "))
}

// GetWritableTmpDir returns the first writable tmp directory under the data dirs: the stable root, for work too large or too long-lived for node-local scratch.
//   - Shared dirs (extra-root, root) create tmp only when the parent already exists.
//   - Personal dirs (scratch, user) always create it on first use.
//   - CNT_TMPDIR does not redirect this. It selects the fast root, and merging the two could put a data build's payload on scratch a job wipes.
func GetWritableTmpDir() string {
	var dirs []SearchDir
	if extraRoot := GetExtraRootDir(); extraRoot != "" {
		dirs = append(dirs, SearchDir{Path: filepath.Join(extraRoot, "tmp")})
	}
	if rootDir := GetRootDir(); rootDir != "" {
		dirs = append(dirs, SearchDir{Path: filepath.Join(rootDir, "tmp")})
	}
	if scratchDir := GetScratchDataDir(); scratchDir != "" {
		dirs = append(dirs, SearchDir{Path: filepath.Join(scratchDir, "tmp"), Personal: true})
	}
	if userDir := GetUserDataDir(); userDir != "" {
		dirs = append(dirs, SearchDir{Path: filepath.Join(userDir, "tmp"), Personal: true})
	}

	if dir := firstWritableDir(deduplicateWriteDirs(dirs)); dir != "" {
		return dir
	}

	// No writable data dir at all: fall back to the fast root rather than a
	// relative path, which would put the workspace wherever the caller stood.
	return utils.GetTmpDir()
}
