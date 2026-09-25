// Package tool runs the external tools image operations shell out to, and
// reports their failures as structured errors.
package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

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

	if err := cmd.Run(); err != nil {
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
