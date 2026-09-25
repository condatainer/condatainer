// Package exec runs an ephemeral container: it sets up the overlays, environment
// and binds, resolves the root and the apptainer binary, holds the image locks,
// and launches.
package exec

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image/sif"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
)

// Options configures how CondaTainer executes a command inside an Apptainer container.
type Options struct {
	Overlays       []string
	Command        []string
	EnvSettings    []string
	BindPaths      []string
	ApptainerFlags []string // Flags to pass directly to apptainer (e.g., --home=/path, --nv)
	Fakeroot       bool
	WritableImg    bool
	HidePrompt     bool
	// Activation selects which activate.d scripts container.ActivationScript
	// sources before the command — container.ActivationAll (default when
	// left ""), container.ActivationEnv, or container.ActivationNone, e.g. so
	// a hung or misbehaving activation script can be ruled out.
	Activation container.ActivationMode
	// GpuRequested forces GPU flag detection even when autoload_gpu is disabled,
	// for a command that explicitly declared a GPU requirement.
	GpuRequested bool

	// BindLibexec binds the libexec toolchain into the container, so a nested
	// condatainer reaches the apptainer installed there.
	BindLibexec bool

	BaseImage string

	// StopGrace is how long the container gets to exit after this process is
	// told to stop; zero keeps the default.
	StopGrace time.Duration

	// PassThruStdin is retained for callers that track whether stdin is expected.
	// Actual stdin is owned by IO.Stdin so internal execution never assumes a terminal.
	PassThruStdin bool
}

// IO contains caller-owned process streams. Nil streams are silent/no input.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type ioContextKey struct{}

// WithIO returns a context carrying caller-owned process streams.
func WithIO(ctx context.Context, ioStreams IO) context.Context {
	return context.WithValue(ctx, ioContextKey{}, ioStreams)
}

// IOFromContext returns caller-owned process streams from ctx, if present.
func IOFromContext(ctx context.Context) IO {
	if ioStreams, ok := ctx.Value(ioContextKey{}).(IO); ok {
		return ioStreams
	}
	return IO{}
}

// IsZero reports whether no streams are set.
func (ioStreams IO) IsZero() bool {
	return ioStreams.Stdin == nil && ioStreams.Stdout == nil && ioStreams.Stderr == nil
}

// ensureDefaults fills in what the caller left blank: a command to run.
//   - BaseImage is resolved separately, after overlay setup — see Prepare — since the exec root may come from the requested overlays.
//   - The apptainer binary is not caller-configurable at all: Prepare picks apptainer.Fakeroot or apptainer.Normal from whether this invocation ends up needing fakeroot.
func (o Options) ensureDefaults() (Options, error) {
	if len(o.Command) == 0 {
		o.Command = []string{"bash"}
	}
	if o.Activation == "" {
		o.Activation = container.ActivationAll
	}
	return o, nil
}

// resolveBaseImage finalizes BaseImage now that Setup has run.
//   - root, an exec root pulled out of the requested overlays, wins when present.
//   - Otherwise the caller's BaseImage is kept, then the configured default.
//   - The default is found, never built. This package runs images. A missing one is the caller's job (internal/build.ResolveBase).
//   - There is no overlay-only execution, so a container with no root is refused here instead of letting Apptainer report a missing file.
//   - A .sif root is also checked for /bin/bash, which nothing else on this path guarantees.
func (o Options) resolveBaseImage(root string) (Options, error) {
	switch {
	case root != "":
		o.BaseImage = root
	case o.BaseImage == "":
		base, err := config.GetBaseImage()
		if err != nil {
			return o, err
		}
		o.BaseImage = base
	}
	// An image file, or a sandbox: apptainer runs either as a root, and a
	// definition build packs its own sandbox by running it.
	if !utils.FileExists(o.BaseImage) && !utils.IsSandboxDir(o.BaseImage) {
		return o, fmt.Errorf("base image not found: %s", o.BaseImage)
	}
	if utils.IsSif(o.BaseImage) {
		if err := sif.RequireBash(o.BaseImage); err != nil {
			return o, err
		}
	}
	return o, nil
}
