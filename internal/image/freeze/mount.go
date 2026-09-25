package freeze

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/condatainer/condatainer/internal/image/tool"
	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
)

// executablePath resolves condatainer's own binary path, for MountedRun to
// re-exec into the sentinel. Overridden in tests: os.Executable() inside `go
// test` returns the test binary, which has no _mount_sentinel command.
var executablePath = os.Executable

// MountedRun mounts fuseBin at mnt in a fresh unprivileged mount+user namespace and runs work in it.
//   - It waits for the mount, runs work (a bash fragment that sees mnt as a directory), then ends the mount by killing the FUSE process, never with umount.
//   - fuseArgs holds the tool's flags. mnt is appended.
//   - It re-execs into the hidden `_mount_sentinel` (RunSentinel), so something outside the namespace survives condatainer dying.
//   - Exported so internal/build can read a .sif the same way.
func MountedRun(ctx context.Context, fuseBin string, fuseArgs []string, mnt string, work string, io execpkg.IO) error {
	if err := tool.CheckDependencies([]string{"unshare"}); err != nil {
		return err
	}

	self, err := executablePath()
	if err != nil {
		return fmt.Errorf("locating condatainer binary: %w", err)
	}

	// "--" stops cobra from parsing fuseArgs as its own flags: an option like
	// squashfuse's "-o" would otherwise be rejected as an unknown shorthand.
	sentinelArgs := append([]string{"_mount_sentinel", fuseBin, mnt, "--"}, fuseArgs...)
	cmd := exec.CommandContext(ctx, self, sentinelArgs...)
	cmd.Env = append(os.Environ(), EnvSentinelWork+"="+work)
	cmd.Stdin = io.Stdin
	cmd.Stdout = io.Stdout
	cmd.Stderr = io.Stderr

	// Setpgid: true (no Pgid) makes the sentinel its own group leader, and
	// RunSentinel joins bash to that same group — so a single group kill,
	// from either direction below, reaches the sentinel, bash, and the
	// backgrounded FUSE process together.
	//
	// Cancel covers a still-running condatainer choosing to cancel: it kills
	// the group directly, same as before. Pdeathsig covers the other
	// failure mode — condatainer dying with no chance to run any Go code at
	// all (SIGKILL, OOM-kill, crash): the kernel delivers SIGTERM to the
	// sentinel directly (Pdeathsig survives here because the sentinel never
	// enters the user namespace itself), and RunSentinel's own trap does the
	// group kill from the inside instead.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	return cmd.Run()
}
