package scheduler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// siblingBin resolves a companion binary name relative to an already-known absolute binary path.
//   - When base is absolute, it checks the same directory (two fast os.Stat calls instead of a full PATH scan).
//   - Falls back to exec.LookPath when base is relative or not absolute.
func siblingBin(base, name string) string {
	if filepath.IsAbs(base) {
		p := filepath.Join(filepath.Dir(base), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return ""
	}
	p, _ := exec.LookPath(name)
	return p
}

// DefaultCommandTimeout is the maximum time to wait for a scheduler command to respond.
// Set this before calling any scheduler methods (e.g., from cmd/root.go after loading config).
// A value of 0 disables the timeout (command runs until completion).
var DefaultCommandTimeout = 0 * time.Second

// JobHome, when non-empty, is the HOME every generated job script sets for itself.
// Set from cmd/root.go's config load (config.ApplyHomeOverride).
var JobHome string

// commandEnv is the environment of a scheduler command: the process's, with the user's own HOME restored when home_override replaced it. nil means inherit.
func commandEnv() []string {
	real := os.Getenv(utils.EnvRealHome)
	if real == "" {
		return nil
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			env = append(env, entry)
		}
	}
	return append(env, "HOME="+real)
}

// schedulerCommand is exec.CommandContext with commandEnv.
func schedulerCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = commandEnv()
	return cmd
}

// runCommand executes a scheduler CLI command with DefaultCommandTimeout.
//   - Returns TimeoutError if the command does not respond within the timeout.
//   - If DefaultCommandTimeout is 0, the command runs without a time limit.
//   - It runs with the user's own HOME, not home_override.
func runCommand(ctx context.Context, schedulerName, operation, bin string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if DefaultCommandTimeout == 0 {
		out, err := schedulerCommand(ctx, bin, args...).CombinedOutput()
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultCommandTimeout)
	defer cancel()
	out, err := schedulerCommand(ctx, bin, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, &TimeoutError{Scheduler: schedulerName, Operation: operation, Timeout: DefaultCommandTimeout}
	}
	return out, err
}
