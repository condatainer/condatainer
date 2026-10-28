package store

import (
	"errors"
	"fmt"
	"os"

	artifactcache "github.com/condatainer/condatainer/internal/artifact/cache"
	"github.com/condatainer/condatainer/internal/image"
)

// resolveIdentityFor locates the artifact to remove. Injected for tests, which
// have no real image for key regeneration to read.
var resolveIdentityFor = ResolveIdentity

// ErrNotStored reports an artifact that resolved to a flat install rather than
// a store entry.
var ErrNotStored = errors.New("overlay is not a store entry")

// Remove deletes one store entry, lock and protection permitting.
//   - Store layout only. A flat artifact answers to a bare name and belongs to `remove`.
//   - ErrInUse: a container is running.
//   - ErrProtected: the identity was pinned by clearing its write bit.
//   - A missing file: a concurrent remover won.
//   - When the entry was found and could not be removed, the candidate is returned with the error, so a caller can say which entry it was.
func Remove(name string, q IdentityQuery, dirs []string) (Candidate, error) {
	candidate, err := storedCandidate(name, q, dirs)
	if err != nil {
		return Candidate{}, err
	}
	if err := removeLocked(candidate.Path); err != nil {
		return candidate, err
	}
	return candidate, nil
}

// Removable finds the entry Remove would delete and checks that it could be, by
// taking and releasing its exclusive lock. Nothing is deleted. The candidate is
// returned with the error when only the lock fails, so a caller can say which
// entry it was.
func Removable(name string, q IdentityQuery, dirs []string) (Candidate, error) {
	candidate, err := storedCandidate(name, q, dirs)
	if err != nil {
		return Candidate{}, err
	}
	lock, err := image.AcquireLock(candidate.Path, true)
	if err != nil {
		return candidate, err
	}
	lock.Close() //nolint:errcheck
	return candidate, nil
}

// storedCandidate resolves the one store entry an address names.
func storedCandidate(name string, q IdentityQuery, dirs []string) (Candidate, error) {
	candidate, _, err := resolveIdentityFor(name, q, dirs)
	if err != nil {
		return Candidate{}, err
	}
	if candidate.Layout != LayoutStored {
		return Candidate{}, fmt.Errorf("%w: %s is installed flat; use `condatainer remove`",
			ErrNotStored, candidate.Path)
	}
	return candidate, nil
}

// removeLocked unlinks one artifact under its exclusive inode lock and forgets it from the artifact cache.
//   - The lock is released after the unlink and before the cache update.
//   - That is the only order that cannot leave a cached record for a live file.
func removeLocked(path string) error {
	lock, err := image.AcquireLock(path, true)
	if err != nil {
		return err
	}
	removeErr := os.Remove(path)
	lock.Close() //nolint:errcheck
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	artifactcache.Forget(path)
	return nil
}
