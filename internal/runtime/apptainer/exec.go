package apptainer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/logging"
)

// ExecOptions contains options for executing commands in a container
type ExecOptions struct {
	Bin        Bin       // The apptainer to run, from Normal or Fakeroot
	Bind       []string  // Bind mounts (format: "/host/path:/container/path")
	Overlay    []string  // Overlay images to use
	Fakeroot   bool      // Run with fakeroot
	Env        []string  // Environment variables to set (format: "KEY=VALUE")
	UnsetEnv   []string  // Host variable names removed from apptainer's environment, so the container never sees them
	Additional []string  // Additional flags to pass to apptainer exec
	Stdin      io.Reader // Custom stdin reader (optional, defaults to os.Stdin)
	Stdout     io.Writer // Redirect stdout (optional; nil = discard)
	Stderr     io.Writer // Redirect stderr (optional; nil = discard)
	// StopGrace is how long apptainer gets to exit after a cancel's SIGTERM
	// before it is killed. Zero keeps the default.
	StopGrace time.Duration
}

// Exec executes a command inside a container
// Note: Bind paths and GPU flags should be processed by the caller (e.g., via container.Setup())
func Exec(ctx context.Context, imagePath string, command []string, opts *ExecOptions) error {
	if opts == nil {
		opts = &ExecOptions{}
	}
	if opts.Bin.Path == "" {
		return ErrNotResolved
	}

	args := []string{"exec"}

	for _, bind := range opts.Bind {
		args = append(args, "--bind", bind)
	}
	for _, overlay := range opts.Overlay {
		args = append(args, "--overlay", overlay)
	}
	if opts.Fakeroot {
		args = append(args, "--fakeroot")
	}

	// Add additional flags (caller should have already included GPU flags if needed)
	args = append(args, opts.Additional...)

	args = append(args, imagePath)
	args = append(args, command...)

	logging.FromContext(ctx).Debug("executing in container", "image", imagePath, "command", strings.Join(command, " "))

	setEnv, unsetEnv := nestedBindEnv(opts.Bind)
	procEnv := append(envPrefixed(opts.Bin, opts.Env), setEnv...)
	unsetEnv = append(slices.Clone(opts.UnsetEnv), unsetEnv...)

	return runApptainerWithOutput(ctx, opts.Bin, "exec", imagePath, false, opts.Stdin, opts.Stdout, opts.Stderr, procEnv, unsetEnv, opts.StopGrace, args...)
}

// inheritedBindVars are the variables apptainer reads binds from. A container's apptainer exports its binds under them.
var inheritedBindVars = []string{"APPTAINER_BIND", "SINGULARITY_BIND", "APPTAINER_BINDPATH", "SINGULARITY_BINDPATH"}

// nestedBindEnv returns the bind variables of a nested launch with the entries already in binds removed.
//   - set holds NAME=value for a variable that keeps some entries. unset names one left with none.
//   - An entry is a duplicate when its container path is one of binds. The launch's own bind wins.
//   - Outside a container nothing is changed.
func nestedBindEnv(binds []string) (set, unset []string) {
	if !insideContainer() {
		return nil, nil
	}
	taken := make(map[string]bool, len(binds))
	for _, b := range binds {
		taken[bindDest(b)] = true
	}
	for _, name := range inheritedBindVars {
		raw := os.Getenv(name)
		if raw == "" {
			continue
		}
		var keep []string
		for _, entry := range strings.Split(raw, ",") {
			if entry = strings.TrimSpace(entry); entry != "" && !taken[bindDest(entry)] {
				keep = append(keep, entry)
			}
		}
		switch joined := strings.Join(keep, ","); {
		case joined == raw:
		case len(keep) == 0:
			unset = append(unset, name)
		default:
			set = append(set, name+"="+joined)
		}
	}
	return set, unset
}

// bindDest is the container path of a host[:container[:opts]] bind.
func bindDest(spec string) string {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) > 1 && parts[1] != "" {
		return filepath.Clean(parts[1])
	}
	return filepath.Clean(parts[0])
}

// envPrefixed rewrites KEY=VALUE settings as APPTAINERENV_KEY=VALUE for the apptainer process's own environment.
//   - It does not use --env, which is parsed with CSV rules. A comma or quote in a value fails the launch, and a signed download URL from an #INPUT: routinely has both.
//   - The prefixed form passes values verbatim.
//   - It also keeps them off the command line, where any user's ps would show them.
func envPrefixed(bin Bin, settings []string) []string {
	if len(settings) == 0 {
		return nil
	}
	prefix := "APPTAINERENV_"
	if bin.IsSingularity() {
		prefix = "SINGULARITYENV_"
	}
	out := make([]string, 0, len(settings))
	for _, setting := range settings {
		key, value, ok := strings.Cut(setting, "=")
		if !ok || key == "" {
			continue
		}
		out = append(out, prefix+key+"="+value)
	}
	return out
}
