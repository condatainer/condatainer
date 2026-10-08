package ext3

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/image/tool"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/toolpath"
)

// CheckIntegrity runs a filesystem check (e2fsck) on the overlay image.
// force: If true, adds '-f' to force the check even if the filesystem appears clean.
// It holds an exclusive lock while the check runs, and fails if the image is in use.
func CheckIntegrity(ctx context.Context, path string, force bool) error {
	lock, err := lockForWrite(path)
	if err != nil {
		return err
	}
	defer lock.Close()
	return checkLocked(ctx, path, force)
}

// lockForWrite takes the exclusive lock on the image, or says why it cannot.
func lockForWrite(path string) (*image.Lock, error) {
	lock, err := image.AcquireLock(path, true)
	if errors.Is(err, image.ErrInUse) {
		return nil, fmt.Errorf("%w: stop any running jobs using it first", err)
	}
	return lock, err
}

// checkLocked runs e2fsck on an image the caller already holds the exclusive lock on.
func checkLocked(ctx context.Context, path string, force bool) error {
	e2fsckPath, err := toolpath.Resolve("e2fsck")
	if err != nil {
		return err
	}

	args := []string{"-p"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, path)

	log := logging.FromContext(ctx)
	log.Info(fmt.Sprintf("Checking integrity of %s", filepath.Base(path)))
	log.Debug("e2fsck " + strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, e2fsckPath, args...)
	out, err := cmd.CombinedOutput()

	// e2fsck exit codes: 0 = clean, 1 = errors corrected, 2+ = critical failure.
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() > 1 {
			return &tool.Error{
				Op:      "check integrity",
				Path:    path,
				Tool:    "e2fsck",
				Output:  string(out),
				BaseErr: err,
			}
		}
	}

	if len(out) > 0 {
		log.Debug("e2fsck output: " + string(out))
	}

	log.Info(fmt.Sprintf("Filesystem check completed for %s", filepath.Base(path)), "kind", "success")
	return nil
}

const (
	superblockOffset = 1024
	stateOffset      = superblockOffset + 58
	magicOffset      = superblockOffset + 56
	magicExt         = 0xEF53
	stateValid       = 0x0001 // set by a clean unmount
	stateErrors      = 0x0002
)

// ShutDownCleanly reports whether the image's superblock says its last mount ended cleanly.
//   - It reads the superblock directly, so no tool runs and nothing is modified.
//   - A mounted image reports false while it is in use.
func ShutDownCleanly(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var sb [4]byte
	if _, err := f.ReadAt(sb[:2], magicOffset); err != nil {
		return false, err
	}
	if binary.LittleEndian.Uint16(sb[:2]) != magicExt {
		return false, fmt.Errorf("%s is not an ext2/3/4 filesystem", filepath.Base(path))
	}
	if _, err := f.ReadAt(sb[:2], stateOffset); err != nil {
		return false, err
	}
	state := binary.LittleEndian.Uint16(sb[:2])
	return state&stateValid != 0 && state&stateErrors == 0, nil
}
