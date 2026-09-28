// Package image holds file operations on overlay images: locking, reading a path
// out of an archive, and finding what is installed. It knows nothing about
// CondaTainer metadata.
package image

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/condatainer/condatainer/internal/utils"
)

// Why a lock attempt failed, so a caller can react rather than only report.
var (
	// ErrProtected marks an image the caller cannot write. See AcquireLock.
	ErrProtected = errors.New("image is write-protected")
	// ErrInUse marks a conflicting lock held by another process.
	ErrInUse = errors.New("image is in use")
)

// lockError is a message written for the reader that still matches its sentinel
// with errors.Is. Wrapping the sentinel with %w would print its text after the
// message, repeating it.
type lockError struct {
	msg  string
	kind error
}

func (e *lockError) Error() string { return e.msg }
func (e *lockError) Unwrap() error { return e.kind }

// Lock represents a file lock on an overlay image. It must be closed to
// release the lock. It is the utils file lock that other packages take on
// their own lock files, so no conversion is needed between them.
type Lock = utils.FileLock

// AcquireLock takes a non-blocking whole-file lock on the overlay image: exclusive when write is true, shared otherwise.
//   - It is the kind of lock Apptainer holds on a mounted ext3 image, so a running container is seen.
//   - A write lock opens O_RDWR, as an fcntl write lock requires.
//   - So an image the caller cannot write is protected. CondaTainer never modifies or removes it.
//   - Clearing every write bit is how an artifact is pinned, even against its owner.
func AcquireLock(path string, write bool) (*Lock, error) {
	lock, err := utils.AcquireFileLock(path, write)
	if err != nil {
		if errors.Is(err, utils.ErrLockConflict) {
			if write {
				return nil, &lockError{path + " is currently in use", ErrInUse}
			}
			return nil, &lockError{path + " is open for writing by another process", ErrInUse}
		}
		return nil, openFailure(path, err, write)
	}
	return lock, nil
}

// openFailure names the actual reason the image could not be opened. Only a
// lock conflict is "in use"; a protected or missing image is neither, and
// reporting one as the other sends the reader looking for a container that is
// not running.
func openFailure(path string, err error, write bool) error {
	styled := path
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s does not exist", styled)
	case errors.Is(err, fs.ErrPermission) && write:
		return &lockError{styled + unwritableReason(path), ErrProtected}
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("%s is not readable", styled)
	}
	return fmt.Errorf("can't open %s: %w", styled, err)
}

// unwritableReason says why path cannot be opened for writing.
//   - With no write bit at all, it is pinned, and chmod +w undoes that.
//   - Otherwise it belongs to someone else, whose chmod it is not the caller's to run.
func unwritableReason(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o222 == 0 {
		return " is write-protected; chmod +w to allow changes"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) == os.Geteuid() {
		return " is write-protected; chmod u+w to allow changes"
	}
	owner := strconv.FormatUint(uint64(st.Uid), 10)
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}
	return " is not writable by you (owner: " + owner + ")"
}

// CheckAvailable reports whether the image can be locked for reading or writing
// right now. It releases immediately, so it is a pre-flight check: a caller that
// must not race another process holds the lock across its own work instead.
func CheckAvailable(path string, write bool) error {
	lock, err := AcquireLock(path, write)
	if err != nil {
		return err
	}
	return lock.Close()
}
