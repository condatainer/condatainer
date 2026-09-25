package freeze

import (
	"errors"
	"fmt"

	"github.com/condatainer/condatainer/internal/toolpath"
)

// ErrNoFuse2fs reports that no fuse2fs could be found to read the overlay with.
var ErrNoFuse2fs = errors.New("no fuse2fs available to read the overlay")

// ErrNoFuseOverlayfs reports that no fuse-overlayfs could be found to layer the image over its snapshot.
var ErrNoFuseOverlayfs = errors.New("no fuse-overlayfs available to layer the overlay over its snapshot")

// ErrNoSquashfuse reports that no squashfuse could be found to read an artifact.
var ErrNoSquashfuse = errors.New("no squashfuse available to read the artifact")

// FindSquashfuse locates the squashfuse that mounts a frozen artifact, for MountedRun to run directly.
//   - toolpath.Resolve prefers libexec's own copy, so one is found even on a host without it.
//   - squashfuse_ll is tried first. libexec provisions both names from one package.
//   - Exported so internal/build can mount a .sif's partition the same way.
func FindSquashfuse() (string, error) {
	for _, name := range []string{"squashfuse_ll", "squashfuse"} {
		if p, err := toolpath.Resolve(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoSquashfuse, toolpath.NotFoundMessage("squashfuse"))
}

// findFuse2fs locates the fuse2fs that will read the image, the same way
// FindSquashfuse locates its counterpart — except libexec never provisions
// it, so NotFoundMessage never suggests `condatainer update --libexec` for it:
// that command would not help.
func findFuse2fs() (string, error) {
	if p, err := toolpath.Resolve("fuse2fs"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoFuse2fs, toolpath.NotFoundMessage("fuse2fs"))
}

// FindFuseOverlayfs locates the fuse-overlayfs that layers an image over its
// snapshot. libexec provisions it, so a missing one is fixed by `condatainer
// update --libexec`. Exported so that command can tell whether one is missing.
func FindFuseOverlayfs() (string, error) {
	if p, err := toolpath.Resolve("fuse-overlayfs"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoFuseOverlayfs, toolpath.NotFoundMessage("fuse-overlayfs"))
}
