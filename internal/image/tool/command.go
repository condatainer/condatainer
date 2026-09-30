// Package tool runs the external tools image operations shell out to, and
// reports their failures as structured errors.
package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/toolpath"
)

// CheckDependencies verifies that every tool in the list resolves through toolpath.Resolve: libexec, then PATH, then the FHS fallback directories.
//   - It returns one error listing all missing tools, or nil.
//   - Each entry is toolpath.NotFoundMessage(tool), not the bare name.
//   - A missing libexec-provisioned tool such as "unsquashfs" points at `condatainer update --libexec`. An e2fsprogs or core-OS tool in the same list does not.
func CheckDependencies(tools []string) error {
	var missing []string

	for _, tool := range tools {
		if _, err := toolpath.Resolve(tool); err != nil {
			missing = append(missing, toolpath.NotFoundMessage(tool))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required system tools: %s",
			strings.Join(missing, "; "))
	}

	return nil
}

// RunCommand executes a shell command and wraps failures in Error. tool is resolved via toolpath.Command (libexec, then PATH, then the FHS fallback directories) rather than handed to exec as a bare name.
//   - When ctx carries a writer (via logging.WithWriter), stdout/stderr are streamed there in real time (web terminal).
//   - Otherwise output is buffered and only surfaced on error (CLI behaviour unchanged).
func RunCommand(ctx context.Context, op, path, tool string, args ...string) error {
	return run(ctx, false, op, path, tool, args)
}

// RunCommandBound is RunCommand for a tool that must not outlive this process: the kernel kills it with SIGKILL when this process dies, even if that death is itself a SIGKILL.
func RunCommandBound(ctx context.Context, op, path, tool string, args ...string) error {
	return run(ctx, true, op, path, tool, args)
}

func run(ctx context.Context, bound bool, op, path, tool string, args []string) error {
	cmd, err := toolpath.Command(ctx, tool, args...)
	if err != nil {
		return &Error{Op: op, Path: path, Tool: tool, BaseErr: err}
	}

	var errBuf bytes.Buffer
	if w := logging.WriterFromCtx(ctx); w != nil {
		out := io.MultiWriter(w, &errBuf)
		cmd.Stdout = out
		cmd.Stderr = out
	} else {
		cmd.Stdout = &errBuf
		cmd.Stderr = &errBuf
	}

	err = start(cmd, bound)
	if err == nil {
		err = cmd.Wait()
	}
	if err != nil {
		return &Error{
			Op:      op,
			Path:    path,
			Tool:    tool,
			Output:  errBuf.String(),
			BaseErr: err,
		}
	}
	return nil
}

// start launches cmd. A bound child is started from a locked OS thread, since the kernel delivers the parent-death signal when that thread exits, not when the process does.
func start(cmd *exec.Cmd, bound bool) error {
	if !bound {
		return cmd.Start()
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	return cmd.Start()
}
