// Package apptainer wraps the Apptainer binary: choosing which one to run,
// launching containers, and building images.
package apptainer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/toolpath"
	"github.com/condatainer/condatainer/internal/utils"
)

// Bin is one resolved apptainer binary. A launch carries the
// Bin its caller resolved, so nothing about which binary runs is package state.
type Bin struct {
	Path string
	Env  []string // Added to the environment of every launch, ahead of the launch's own
}

// insideContainer is config.IsInsideContainer, replaceable by tests.
var insideContainer = config.IsInsideContainer

// hostBin returns the host_apptainer, which is the host's only apptainer: none on PATH is used.
func hostBin() (Bin, error) {
	path := toolpath.HostApptainer()
	if path == "" {
		return Bin{}, &ApptainerNotFoundError{}
	}
	full, err := exec.LookPath(path)
	if err != nil {
		return Bin{}, &ApptainerNotFoundError{Path: path}
	}
	return Bin{Path: full}, nil
}

// unusable words why no apptainer could be used, for where this process runs.
// A container cannot install one, so it is told how to get one at launch.
func unusable(err error) error {
	if insideContainer() {
		return errors.New("this container has no apptainer; install the toolchain with `condatainer update --libexec` on the host, then start the container again")
	}
	if err == nil {
		err = &ApptainerNotFoundError{}
	}
	return fmt.Errorf("no apptainer is available (%w); install one with `condatainer update --libexec`, or set host_apptainer", err)
}

// ErrHostApptainerUnusable reports a host_apptainer that is set but cannot be run.
// It is never replaced by the toolchain's apptainer, since which one runs is the user's choice.
var ErrHostApptainerUnusable = errors.New("host_apptainer is not usable")

// Where an apptainer comes from, as Selected names it.
const (
	SourceHost      = "host_apptainer"
	SourceToolchain = "toolchain"
)

// Selected returns the apptainer a launch would use and where it comes from, or "" when there is none.
//   - It is the host_apptainer when set, else the one installed in libexec. A host_apptainer that cannot be run selects none.
//   - Inside a container only libexec's is used, since the host's cannot be assumed to be there or configured.
//   - It runs nothing, so it does not check the version or the fakeroot.
func Selected() (path, source string) {
	if !insideContainer() {
		if host := toolpath.HostApptainer(); host != "" {
			return host, SourceHost
		}
		if toolpath.InvalidHostApptainer() != "" {
			return "", ""
		}
	}
	if path, ok := libexec.ApptainerPath(); ok {
		return path, SourceToolchain
	}
	return "", ""
}

// Need is what a launch asks of its apptainer.
type Need struct {
	Fakeroot bool // The launch uses --fakeroot
	Overlays bool // The launch mounts condatainer's overlays, which are zstd-compressed
}

// Resolve returns the apptainer Selected names, checked for the launch's need.
//   - Overlays refuses a host apptainer below the zstd floor.
//   - Fakeroot: a non-setuid host apptainer that ships its own fakeroot is replaced by libexec's, when installed, with a warning.
//     The toolchain's fakeroot goes first on PATH, carried in Bin.Env.
func Resolve(need Need) (Bin, error) {
	if bad := toolpath.InvalidHostApptainer(); bad != "" && !insideContainer() {
		return Bin{}, fmt.Errorf("%w: %q is not an executable file or a command on PATH. Fix it, or remove it with `condatainer config remove host_apptainer`", ErrHostApptainerUnusable, bad)
	}
	path, source := Selected()
	switch source {
	case "":
		return Bin{}, unusable(nil)
	case SourceToolchain:
		return withFakeroot(Bin{Path: path}, need), nil
	}
	bin, err := hostBin()
	if err != nil {
		return Bin{}, unusable(err)
	}
	if libexecPath, ok := libexec.ApptainerPath(); ok && need.Fakeroot && bin.shipsOwnFakeroot() {
		warnOwnFakeroot(bin.Path)
		return withFakeroot(Bin{Path: libexecPath}, need), nil
	}
	if need.Overlays {
		if err := bin.requireZstd("exec"); err != nil {
			return Bin{}, unusable(err)
		}
	}
	return withFakeroot(bin, need), nil
}

// fuse2fsProblem is replaceable by tests.
var fuse2fsProblem = toolpath.Fuse2fsProblem

// CheckWritableImg refuses a writable .img mount when the fuse2fs the apptainer would mount with
// would corrupt the image, and says how to get one that does not.
//   - The apptainer installed in libexec mounts with the fuse2fs beside it, which is not checked.
//   - Any other apptainer takes the fuse2fs in its own tool directory, else on PATH.
func (b Bin) CheckWritableImg() error {
	installed, ok := libexec.ApptainerPath()
	if ok && installed == b.Path {
		return nil
	}
	path, err := toolpath.ResolveFor(b.Path, "fuse2fs")
	if err != nil {
		return nil // Apptainer reports a missing fuse2fs itself
	}
	problem := fuse2fsProblem(path)
	if problem == "" {
		return nil
	}
	if ok {
		return fmt.Errorf("%s. Unset host_apptainer to use the toolchain's apptainer, which ships a fixed fuse2fs", problem)
	}
	return fmt.Errorf("%s. Run `condatainer update --libexec`, then unset host_apptainer, to use the toolchain's apptainer, which ships a fixed fuse2fs", problem)
}

// requireZstd refuses an apptainer that cannot mount
// condatainer's zstd-compressed overlays. what names the caller in the message.
func (b Bin) requireZstd(what string) error {
	version, err := b.Version()
	if err != nil {
		return fmt.Errorf("could not determine the version of %s: %w", b.Path, err)
	}
	if !CheckZstdSupport(version) {
		return fmt.Errorf("%s needs apptainer >= 1.4 to mount zstd-compressed overlays; %s is %s", what, b.Path, version)
	}
	return nil
}

// ErrNotResolved reports a launch given no apptainer binary.
var ErrNotResolved = errors.New("no apptainer binary has been resolved yet")

// versions caches each binary's version for the process, by path.
var versions sync.Map

var versionPattern = regexp.MustCompile(`(\d+\.\d+(\.\d+)?)`)

// Version returns the binary's version. It runs the binary only when this
// process and the per-user cache (unchanged size and modification time) have
// no answer for its path.
func (b Bin) Version() (string, error) {
	if cached, ok := versions.Load(b.Path); ok {
		return cached.(string), nil
	}
	version, err := toolpath.Remember("version", b.Path, b.runVersion)
	if err != nil {
		return "", err
	}
	versions.Store(b.Path, version)
	return version, nil
}

// runVersion runs the binary's --version and parses the number out.
func (b Bin) runVersion() (string, error) {
	// exec.Command directly: the output string is needed, and
	// runApptainerWithOutput returns only an error.
	cmd := exec.Command(b.Path, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", &ApptainerError{
			Op:      "check version",
			Cmd:     fmt.Sprintf("%s --version", b.Path),
			Output:  "", // Output is often empty on binary execution failure
			BaseErr: err,
		}
	}

	outStr := strings.TrimSpace(string(output))
	match := versionPattern.FindString(outStr)
	if match == "" {
		return "", fmt.Errorf("could not parse version from output: %s", outStr)
	}
	return match, nil
}

// CheckZstdSupport checks if the *current* version supports ZSTD.
func CheckZstdSupport(currentVersion string) bool {
	parts := strings.Split(currentVersion, ".")
	if len(parts) < 2 {
		return false
	}

	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])

	// Apptainer supported ZSTD starting around version 1.4
	if major > 1 || (major == 1 && minor >= 4) {
		return true
	}

	return false
}

// ---------------------------------------------------------
// Internal Helper
// ---------------------------------------------------------

// defaultStopGrace is how long apptainer gets to exit after SIGTERM before it
// is killed.
const defaultStopGrace = 5 * time.Second

// runApptainerWithOutput executes an apptainer command with control over output handling.
//   - op is "exec", "pull", "build" and so on. imagePath is optional and names the container in logs.
//   - capture stores stdout and stderr on the error.
//   - stdin is optional. nil means no stdin.
//   - stdout and stderr redirect the streams, and nil discards them. Both are teed to an internal buffer, so ApptainerError.Output is filled on failure either way.
//   - procEnv is extra KEY=VALUE settings for apptainer's own environment, on top of the parent's. This is how APPTAINERENV_* reaches the container.
//   - unsetEnv names variables removed from the inherited environment before procEnv is applied.
//   - stopGrace is how long apptainer gets after a cancel's SIGTERM before it is killed. Zero means defaultStopGrace.
//   - apptainer runs with -q unless debug mode is on, so its INFO lines are hidden and its warnings and errors still print.
func runApptainerWithOutput(ctx context.Context, bin Bin, op string, imagePath string, capture bool, stdin io.Reader, stdout, stderr io.Writer, procEnv, unsetEnv []string, stopGrace time.Duration, args ...string) error {
	if !utils.DebugMode {
		args = append([]string{"-q"}, args...)
	}
	cmd := exec.CommandContext(ctx, bin.Path, args...)

	cmd.Stdin = stdin

	env := os.Environ()

	// A build drops every inherited bind, so %post does not see the host's mounts.
	if op == "build" {
		unsetEnv = append(slices.Clone(unsetEnv), inheritedBindVars...)
	}

	if len(unsetEnv) > 0 {
		kept := make([]string, 0, len(env))
		for _, e := range env {
			key, _, _ := strings.Cut(e, "=")
			if !slices.Contains(unsetEnv, key) {
				kept = append(kept, e)
			}
		}
		env = kept
	}

	cmd.Env = append(append(env, bin.Env...), procEnv...)

	var stdoutBuf, stderrBuf bytes.Buffer
	var stdoutWriter, stderrWriter io.Writer

	if capture {
		stdoutWriter = &stdoutBuf
		stderrWriter = &stderrBuf
	} else {
		// Tee custom writers with error buffers so ApptainerError.Output
		// is populated on failure when doing so does not hide a real TTY from
		// the child process. Interactive CLI callers pass *os.File streams, and
		// those must be attached directly so shells and tools can detect width,
		// colors, job control, etc.
		if stdout != nil {
			if f, ok := stdout.(*os.File); ok {
				stdoutWriter = f
			} else {
				stdoutWriter = io.MultiWriter(stdout, &stdoutBuf)
			}
		} else {
			stdoutWriter = io.Discard
		}
		if stderr != nil {
			if f, ok := stderr.(*os.File); ok {
				stderrWriter = f
			} else {
				stderrWriter = io.MultiWriter(stderr, &stderrBuf)
			}
		} else {
			stderrWriter = io.Discard
		}
	}

	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	cmd.WaitDelay = defaultStopGrace
	if stopGrace > 0 {
		cmd.WaitDelay = stopGrace
	}
	cmd.Cancel = func() error {
		// Use SIGTERM first for graceful shutdown
		return cmd.Process.Signal(syscall.SIGTERM)
	}

	err := cmd.Run()
	if err != nil {
		fullCmd := fmt.Sprintf("%s %s", bin.Path, strings.Join(args, " "))
		failure := &ApptainerError{
			Op:         op,
			Cmd:        fullCmd,
			Path:       imagePath,
			Output:     captureOutput(&stdoutBuf, &stderrBuf),
			HideOutput: !capture,
			BaseErr:    err,
		}
		if (op == "exec" || op == "build") && refusedLaunch(bin, failure) {
			return blockedError(bin)
		}
		return failure
	}
	return nil
}

func captureOutput(stdoutBuf, stderrBuf *bytes.Buffer) string {
	combined := strings.TrimSpace(stdoutBuf.String() + stderrBuf.String())
	return combined
}

// IsBuildCancelled returns true when the Apptainer error looks like the
// user declined to continue after being asked about overwriting an existing
// build target, or when the build was interrupted by the user (Ctrl+C).
func IsBuildCancelled(err error) bool {
	// Check for context cancellation (Ctrl+C)
	if errors.Is(err, context.Canceled) {
		return true
	}

	// Check for signal interrupts in error message
	errMsg := err.Error()
	if strings.Contains(errMsg, "signal: interrupt") || strings.Contains(errMsg, "signal: killed") {
		return true
	}

	// Check for SIGINT exit code (130 = 128 + 2)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 130 || exitErr.ExitCode() == -1 {
			return true
		}
	}

	// Check for interactive cancellation (user declining to overwrite)
	var apErr *ApptainerError
	if !errors.As(err, &apErr) {
		return false
	}

	output := strings.ToLower(apErr.Output)
	if output == "" {
		return false
	}

	return strings.Contains(output, "build target") &&
		strings.Contains(output, "do you want to continue")
}
