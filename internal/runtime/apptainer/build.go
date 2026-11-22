package apptainer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/utils"
)

// BuildOptions contains options for building a container image
type BuildOptions struct {
	Bin        Bin      // The apptainer to run, from Resolve
	Force      bool     // Force overwrite of existing image
	NoCleanup  bool     // Do not clean up bundle after failed build
	Sandbox    bool     // Write a sandbox directory instead of a SIF
	TmpDir     string   // APPTAINER_TMPDIR for this build; "" leaves the environment alone
	Additional []string // Additional flags to pass to apptainer build
}

// Build builds a container image from a definition file
// Always uses --fakeroot (required for non-root users on HPC systems)
func Build(ctx context.Context, imagePath, defFile string, opts *BuildOptions) error {
	if opts == nil {
		opts = &BuildOptions{}
	}
	if opts.Bin.Path == "" {
		return ErrNotResolved
	}

	args := []string{"build"}

	args = append(args, "--fakeroot")

	if opts.Sandbox {
		args = append(args, "--sandbox")
	}

	// Add optional flags
	if opts.Force {
		args = append(args, "--force")
	}
	if opts.NoCleanup {
		args = append(args, "--no-cleanup")
	}

	// Add user-provided extras. GPU is never detected for a build, requested or not.
	args = append(args, opts.Additional...)

	// Add image path and definition file
	args = append(args, imagePath, defFile)

	logging.FromContext(ctx).Debug("building container", "image", imagePath, "definition", defFile)

	procEnv := buildEnv(opts)

	// Stderr goes through a writer that is not a file, so a failed build keeps its output to read.
	err := runApptainerWithOutput(ctx, opts.Bin, "build", imagePath, false, os.Stdin, os.Stdout, passthrough{os.Stderr}, procEnv, nil, 0, args...)
	return explainFakeroot(err)
}

// passthrough writes to w and is never a *os.File, so a launch tees its output instead of attaching w directly.
type passthrough struct{ w io.Writer }

func (p passthrough) Write(b []byte) (int, error) { return p.w.Write(b) }

// ErrFakerootTooNew reports a build whose fakeroot needs a newer glibc than the image has.
var ErrFakerootTooNew = errors.New("the fakeroot bound into the build needs a newer glibc than the image has")

// tooNewFakeroot matches the loader's refusal of the fakeroot daemon or library inside the build.
var tooNewFakeroot = regexp.MustCompile(`GLIBC[^']*' not found \(required by [^)]*libs/(faked|fakeroot|libfakeroot)`)

// explainFakeroot adds what to do to a build error whose output shows the fakeroot could not load.
func explainFakeroot(err error) error {
	var failure *ApptainerError
	if !errors.As(err, &failure) || !tooNewFakeroot.MatchString(failure.Output) {
		return err
	}
	hint := "run `condatainer update --libexec`, which installs a fakeroot that runs in any image"
	if _, ok := libexec.Path("fakeroot-sysv"); ok {
		hint = "unset host_apptainer to build with the toolchain's apptainer"
	}
	return fmt.Errorf("%w: %s (%w)", ErrFakerootTooNew, hint, err)
}

// buildEnv is what a build adds to the inherited environment.
// APPTAINER_TMPDIR is set when opts.TmpDir is. Left unset it inherits TMPDIR, which on a scheduler is routinely network scratch, and a sandbox assembles beside its destination.
func buildEnv(opts *BuildOptions) []string {
	if opts.TmpDir == "" {
		return nil
	}
	return []string{"APPTAINER_TMPDIR=" + opts.TmpDir}
}

// withFakeroot adds the toolchain's fakeroot to bin's environment when need asks for fakeroot.
//   - It goes first on PATH. An unprivileged apptainer binds the fakeroot it finds into the launch, and a host's is built for the host's glibc, which an older image cannot load.
//   - Without the toolchain, PATH is left alone.
func withFakeroot(bin Bin, need Need) Bin {
	if !need.Fakeroot {
		return bin
	}
	if path, ok := libexec.Path("fakeroot-sysv"); ok {
		bin.Env = []string{"PATH=" + filepath.Dir(path) + string(os.PathListSeparator) + os.Getenv("PATH")}
	}
	return bin
}

// shipsOwnFakeroot reports whether b is a non-setuid install with a fakeroot in its own tool directory.
//   - Its tool directory is searched before PATH, so the toolchain's fakeroot cannot replace that one.
//   - A setuid install does not run fakeroot. An apptainer that will not print its build configuration is taken to have none.
func (b Bin) shipsOwnFakeroot() bool {
	out, err := exec.Command(b.Path, "buildcfg").Output()
	if err != nil {
		return false
	}
	var libexecDir string
	for _, line := range strings.Split(string(out), "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch {
		case key == "LIBEXECDIR":
			libexecDir = value
		case strings.HasSuffix(key, "_SUID_INSTALL") && value == "1":
			return false
		}
	}
	if libexecDir == "" {
		return false
	}
	return utils.FileExists(filepath.Join(libexecDir, filepath.Base(b.Path), "bin", "fakeroot-sysv"))
}

var warnedFakeroot sync.Map

// warnOwnFakeroot tells once per binary that its build is replaced.
func warnOwnFakeroot(path string) {
	if _, seen := warnedFakeroot.LoadOrStore(path, true); seen {
		return
	}
	utils.PrintWarning("%s ships its own fakeroot, which an image with an older glibc may not load. Building with the toolchain's apptainer.", path)
}
