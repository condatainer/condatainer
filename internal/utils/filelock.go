package utils

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLockConflict marks a non-blocking lock attempt that lost to another
// holder: the file opened fine, but a conflicting lock is already held.
var ErrLockConflict = errors.New("file is locked by another process")

// FileLock is an open file held under a non-blocking whole-file lock. It must
// be closed to release the lock.
type FileLock struct {
	file *os.File
}

// Close releases the lock by closing the file.
func (h *FileLock) Close() error {
	if h.file == nil {
		return nil
	}
	err := h.file.Close()
	h.file = nil
	return err
}

// AcquireFileLock opens path and takes a non-blocking whole-file lock: exclusive when write is true, shared otherwise.
//   - write also picks the open mode, O_RDWR or O_RDONLY. Some callers use the open mode as a permission check.
//   - The lock is an fcntl open-file-description lock (Linux 3.15+). It belongs to the descriptor, so two acquisitions in one process conflict and closing the file releases it.
//   - It conflicts with the plain fcntl locks apptainer holds on a mounted ext3 image.
//   - A failed open returns the raw *fs.PathError, so callers can match fs.ErrNotExist or fs.ErrPermission.
//   - A held lock wraps ErrLockConflict. Any other lock failure is returned as is.
func AcquireFileLock(path string, write bool) (*FileLock, error) {
	flag, ltype := os.O_RDONLY, int16(unix.F_RDLCK)
	if write {
		flag, ltype = os.O_RDWR, unix.F_WRLCK
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	lock := unix.Flock_t{Type: ltype, Whence: io.SeekStart}
	if err := unix.FcntlFlock(f.Fd(), unix.F_OFD_SETLK, &lock); err != nil {
		f.Close()
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EACCES) {
			return nil, fmt.Errorf("%w: %w", ErrLockConflict, err)
		}
		return nil, fmt.Errorf("cannot lock %s: %w", path, err)
	}
	return &FileLock{file: f}, nil
}
