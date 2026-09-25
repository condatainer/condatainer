package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// stagingPrefix marks a directory or file mid-write. A dotted name keeps it out
// of the artifact listing, so an interrupted write is invisible rather than a
// half-formed artifact.
const stagingPrefix = "."

// stagingName builds a unique sibling name. Uniqueness only has to survive
// concurrent writers to one project, which pid and nanosecond give.
func stagingName(kind string) string {
	return fmt.Sprintf("%s%s-%d-%d", stagingPrefix, kind, os.Getpid(), time.Now().UnixNano())
}

// ignoreFile keeps machine-local dot directories under cnt-lock/ out of git.
const ignoreFile = ".gitignore"

// EnsureIgnore writes cnt-lock/.gitignore, ignoring every dot directory
// beneath it, unless the file already exists. A file someone edited is kept.
func EnsureIgnore(root string) error {
	path := filepath.Join(Dir(root), ignoreFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	f, err := utils.CreateFileWritable(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("/.*/\n")
	return err
}

// Publish writes a lock atomically, then removes provenance entries no published pin reaches.
//   - The order is the transaction: marshal (which validates), write a temporary sibling, rename, and only then delete.
//   - A failure before the rename leaves the previous lock authoritative, and pruning never runs against a lock that was not published.
func Publish(root string, l *Lock) error {
	// Before Marshal, not after: a remote addresses an artifact directory, so
	// dropping one is an edit to the document rather than to the filesystem. Done
	// in Prune it would tidy the in-memory lock and leave the published bytes
	// still naming an artifact nothing vendors, which Verify calls a problem.
	verified, _ := Verify(root, l)
	pruneRemotes(l, verified)

	data, err := l.Marshal()
	if err != nil {
		return err
	}
	dir := Dir(root)
	if err := utils.MkdirAllShared(dir); err != nil {
		return err
	}
	if err := EnsureIgnore(root); err != nil {
		return err
	}

	staged := filepath.Join(dir, stagingName("lock")+".json")
	defer os.Remove(staged) //nolint:errcheck

	temp, err := utils.CreateFileWritable(staged)
	if err != nil {
		return fmt.Errorf("cannot stage the lock: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close() //nolint:errcheck
		return fmt.Errorf("cannot write the lock: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close() //nolint:errcheck
		return fmt.Errorf("cannot flush the lock: %w", err)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(staged, FilePath(root)); err != nil {
		return fmt.Errorf("cannot publish the lock: %w", err)
	}
	utils.ShareWithParentGroup(FilePath(root))

	return Prune(root, l)
}

// Prune removes provenance entries the published lock does not reach. It edits the
// filesystem only; Publish reconciles remotes first.
//
//   - Reachability is the closure, recomputed from the published file, not the pin set.
//   - An entry that fails to verify is left alone: an unreadable entry cannot be shown unreachable.
func Prune(root string, l *Lock) error {
	present, err := listEntryDirs(root)
	if err != nil {
		return err
	}
	if len(present) == 0 {
		return nil
	}
	verified, _ := Verify(root, l)

	for _, relative := range present {
		if verified.Reachable[relative] {
			continue
		}
		if _, readable := verified.Entries[relative]; !readable {
			continue
		}
		if err := os.RemoveAll(filepath.Join(Dir(root), relative)); err != nil {
			return fmt.Errorf("cannot prune %s: %w", relative, err)
		}
	}
	return nil
}

// pruneRemotes drops fetch locations for artifacts the lock no longer reaches.
//   - Verify reports a remote naming an absent artifact, so keeping one would fail the project's own validate after a re-pin.
//   - Nothing is kept for later: a re-pin at the same identity gets its remote from the same push or upstream walk again.
func pruneRemotes(l *Lock, verified *Verified) {
	for artifact := range l.Remotes {
		if !verified.Reachable[artifact] {
			delete(l.Remotes, artifact)
		}
	}
	if len(l.Remotes) == 0 {
		l.Remotes = nil
	}
}

// StageEntry copies a prepared artifact directory into cnt-lock/provenance/ under
// its final name, atomically: it is built as a sibling and renamed into place. An
// existing entry is left as it is, since one (name, identity) has one set of records.
func StageEntry(root, entryName string, files map[string][]byte) (string, error) {
	if entryName != filepath.Base(entryName) || entryName == "." || entryName == ".." {
		return "", fmt.Errorf("%w: artifact entry %q is not a plain name", ErrInvalid, entryName)
	}
	relative := EntryPath(entryName)
	if err := validEntryPath(relative); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	final := filepath.Join(Dir(root), relative)
	if _, err := os.Stat(final); err == nil {
		return relative, nil
	}
	if err := utils.MkdirAllShared(ProvenancePath(root)); err != nil {
		return "", err
	}

	staging := filepath.Join(ProvenancePath(root), stagingName("staging"))
	if err := utils.MkdirAllShared(staging); err != nil {
		return "", fmt.Errorf("cannot stage %s: %w", entryName, err)
	}
	defer os.RemoveAll(staging) //nolint:errcheck

	for name, data := range files {
		if name != filepath.Base(name) || name == "." || name == ".." {
			return "", fmt.Errorf("%w: source file %q is not a plain name", ErrInvalid, name)
		}
		if len(data) > MaxSourceBytes {
			return "", fmt.Errorf("%w: source %s is %d bytes, over the %d limit", ErrInvalid, name, len(data), MaxSourceBytes)
		}
		file, err := utils.CreateFileWritable(filepath.Join(staging, name))
		if err != nil {
			return "", err
		}
		if _, err := file.Write(data); err != nil {
			file.Close() //nolint:errcheck
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
	}

	if err := os.Rename(staging, final); err != nil {
		// A concurrent writer producing the same (name, identity) wins; its
		// bytes are ours by construction.
		if _, statErr := os.Stat(final); statErr == nil {
			return relative, nil
		}
		return "", fmt.Errorf("cannot publish %s: %w", entryName, err)
	}
	utils.ShareWithParentGroup(final)
	return relative, nil
}
