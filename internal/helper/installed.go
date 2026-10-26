package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

const (
	installedFile   = ".installed.json"
	installedLock   = ".installed.lock"
	installedSchema = 1

	// installedWait bounds how long an update waits for another writer.
	installedWait = 10 * time.Second
)

// ErrInstalledSchema reports an installed record this build does not read.
var ErrInstalledSchema = errors.New("helper: unsupported installed record")

// InstalledEntry is one helper that `add` put in a directory.
type InstalledEntry struct {
	Source string `json:"source"` // handle of the source it came from
	SHA256 string `json:"sha256"` // of the script as added, hex
}

// Installed is the record of the helpers added to one helper-scripts directory.
type Installed struct {
	SchemaVersion int                       `json:"schema_version"`
	Helpers       map[string]InstalledEntry `json:"helpers"`
}

// ReadInstalled reads dir's record. A directory with none has an empty one.
//   - The version is read first. A newer one says to update.
func ReadInstalled(dir string) (*Installed, error) {
	data, err := os.ReadFile(filepath.Join(dir, installedFile))
	if os.IsNotExist(err) {
		return &Installed{SchemaVersion: installedSchema, Helpers: map[string]InstalledEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var rec Installed
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInstalledSchema, installedFile, err)
	}
	if rec.SchemaVersion != installedSchema {
		hint := ""
		if rec.SchemaVersion > installedSchema {
			hint = "; update CondaTainer"
		}
		return nil, fmt.Errorf("%w: %s is schema_version %d (this build reads %d)%s",
			ErrInstalledSchema, filepath.Join(dir, installedFile), rec.SchemaVersion, installedSchema, hint)
	}
	if rec.Helpers == nil {
		rec.Helpers = map[string]InstalledEntry{}
	}
	return &rec, nil
}

// Lookup returns the entry for name, or false when none is recorded or its file is gone.
func (r *Installed) Lookup(dir, name string) (InstalledEntry, bool) {
	entry, ok := r.Helpers[name]
	if !ok {
		return InstalledEntry{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		return InstalledEntry{}, false
	}
	return entry, true
}

// UpdateInstalled reads dir's record under a lock, applies change, and writes it back atomically.
//   - Two writers, such as the CLI and the dashboard, each see the other's entry.
//   - A failed change writes nothing.
func UpdateInstalled(dir string, change func(*Installed) error) error {
	if err := utils.MkdirAllShared(dir); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, installedLock)
	if f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, utils.PermFile); err == nil {
		f.Close() //nolint:errcheck
	} else {
		return err
	}
	lock, err := lockInstalled(lockPath)
	if err != nil {
		return err
	}
	defer lock.Close() //nolint:errcheck

	rec, err := ReadInstalled(dir)
	if err != nil {
		return err
	}
	if err := change(rec); err != nil {
		return err
	}
	return writeInstalled(dir, rec)
}

func lockInstalled(path string) (*utils.FileLock, error) {
	deadline := time.Now().Add(installedWait)
	for {
		lock, err := utils.AcquireFileLock(path, true)
		if !errors.Is(err, utils.ErrLockConflict) {
			return lock, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another process is updating %s", installedFile)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writeInstalled(dir string, rec *Installed) error {
	rec.SchemaVersion = installedSchema
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	staged := filepath.Join(dir, fmt.Sprintf(".installed-%d-%d.json", os.Getpid(), time.Now().UnixNano()))
	defer os.Remove(staged) //nolint:errcheck
	f, err := utils.CreateFileWritable(staged)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	path := filepath.Join(dir, installedFile)
	if err := os.Rename(staged, path); err != nil {
		return err
	}
	utils.ShareWithParentGroup(path)
	return nil
}
