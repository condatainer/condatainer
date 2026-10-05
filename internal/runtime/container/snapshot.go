package container

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/utils"
)

// SnapshotLookup is what LookupSnapshot found beside a writable .img.
type SnapshotLookup struct {
	// Path is the env-typed snapshot to pair with the .img, or "" if none.
	Path string
	// Blocked is the candidate path when the first slot that exists on disk
	// is not an env-typed artifact — occupied by something unrelated, or a
	// mistake. Path is empty whenever Blocked is set.
	Blocked string
}

// LookupSnapshot finds the env-typed .sqf that pairs with a writable .img. It is the one place this pairing is computed, used to autoload a snapshot at mount time and to find what a bare `overlay freeze` replaces.
//   - There is one slot, SnapshotPath: the .img's name without a trailing "-<user>", plus .sqf.
//   - If a file is there and its runtime.json reports catalog.TypeEnv, it is the pair.
//   - Any other file there is Blocked. The mount path treats it as nothing found, and `overlay freeze` refuses to replace it.
func LookupSnapshot(imgPath string) SnapshotLookup {
	slot := SnapshotPath(imgPath)
	if !utils.FileExists(slot) {
		return SnapshotLookup{}
	}
	rt, err := meta.ReadRuntime(slot)
	if err != nil || rt.Type != catalog.TypeEnv {
		return SnapshotLookup{Blocked: slot}
	}
	return SnapshotLookup{Path: slot}
}

// SnapshotPath is where imgPath's snapshot lives, whether or not one is there.
//   - It sits beside the .img and is shared: env-alice.img and env.img both pair with env.sqf.
func SnapshotPath(imgPath string) string {
	return filepath.Join(filepath.Dir(imgPath), snapshotStem(imgPath)+".sqf")
}

// snapshotStem is an .img's basename with its extension and, if present, its
// trailing "-<user>" suffix removed, so "rnaseq-alice.img" and "rnaseq.img"
// share one snapshot.
func snapshotStem(imgPath string) string {
	base := strings.TrimSuffix(filepath.Base(imgPath), filepath.Ext(imgPath))
	if user := os.Getenv("USER"); user != "" && strings.HasSuffix(base, "-"+user) {
		return strings.TrimSuffix(base, "-"+user)
	}
	return base
}

// PairedSize returns the combined size, in bytes, of path and — when path is
// a writable .img that autoloads one (LookupSnapshot) — its paired snapshot,
// plus the snapshot path itself ("" when there is none). Either file may be
// absent; a missing one simply contributes zero, which is what lets this
// double as the third-state probe: no .img on disk yet, but a snapshot's own
// size and path reported anyway.
func PairedSize(path string) (sizeBytes int64, snapshot string) {
	if info, err := os.Stat(path); err == nil {
		sizeBytes = info.Size()
	}
	if utils.IsImg(path) {
		if lookup := LookupSnapshot(path); lookup.Path != "" {
			snapshot = lookup.Path
			if info, err := os.Stat(snapshot); err == nil {
				sizeBytes += info.Size()
			}
		}
	}
	return sizeBytes, snapshot
}

// isEnvSnapshotSqf reports whether path is a .sqf recording Type == TypeEnv.
func isEnvSnapshotSqf(path string) bool {
	if !utils.IsSqf(path) {
		return false
	}
	rt, err := meta.ReadRuntime(path)
	return err == nil && rt.Type == catalog.TypeEnv
}
