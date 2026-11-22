package freeze

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/condatainer/condatainer/internal/toolpath"
)

// ErrNoFuse2fs reports that no fuse2fs could be found to read the overlay with.
var ErrNoFuse2fs = errors.New("no fuse2fs available to read the overlay")

// ErrNoFuseOverlayfs reports that no fuse-overlayfs could be found to layer the image over its snapshot.
var ErrNoFuseOverlayfs = errors.New("no fuse-overlayfs available to layer the overlay over its snapshot")

// ErrNoSquashfuse reports that no squashfuse could be found to read an artifact.
var ErrNoSquashfuse = errors.New("no squashfuse available to read the overlay")

// FindSquashfuse locates the squashfuse that mounts a frozen artifact, for MountedRun to run directly.
//   - toolpath.Resolve prefers libexec's own copy, so one is found even on a host without it.
//   - squashfuse_ll is tried first. libexec provides that one; a host may have only squashfuse.
//   - Exported so internal/build can mount a .sif's partition the same way.
func FindSquashfuse() (string, error) {
	for _, name := range []string{"squashfuse_ll", "squashfuse"} {
		if p, err := toolpath.Resolve(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoSquashfuse, toolpath.NotFoundMessage("squashfuse_ll"))
}

// findFuse2fs locates the fuse2fs that will read the image, the same way
// FindSquashfuse locates its counterpart.
func findFuse2fs() (string, error) {
	if p, err := toolpath.Resolve("fuse2fs"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoFuse2fs, toolpath.NotFoundMessage("fuse2fs"))
}

// FindFuseOverlayfs locates the fuse-overlayfs that layers an image over its
// snapshot. A missing one is fixed by `condatainer update --libexec apptainer`, run on the host.
func FindFuseOverlayfs() (string, error) {
	if p, err := toolpath.Resolve("fuse-overlayfs"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoFuseOverlayfs, toolpath.NotFoundMessage("fuse-overlayfs"))
}

// fuse2fsMount is the read-only mount of an overlay image at path.
//   - fuse2fs checks permissions as the user who mounted it. A job runs as that user, so it would refuse a file whose owner is another uid and whose mode excludes everyone else.
//   - With -o fakeroot it checks as root, and every file reads. A fuse2fs without the option is mounted without it, and the pack refuses a file it could not read.
func fuse2fsMount(bin, img, path string) Mount {
	args := []string{"-o", "ro"}
	if help, _ := exec.Command(bin, "-h").CombinedOutput(); strings.Contains(string(help), "-o fakeroot") {
		args = append(args, "-o", "fakeroot")
	}
	return Mount{Bin: bin, Args: append(args, img), Path: path}
}
