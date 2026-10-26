package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

var (
	ErrNotOffered   = errors.New("helper: not offered by any source")
	ErrAlreadyAdded = errors.New("helper: already added")
	ErrNotAdded     = errors.New("helper: not added")
	ErrUnrecorded   = errors.New("helper: file was not installed by add")
	ErrHashMismatch = errors.New("helper: script does not match its index hash")
	ErrInUse        = errors.New("helper: a run is in progress")
)

// UpgradeAction is what an upgrade does with one added helper.
type UpgradeAction string

const (
	UpgradeReplace    UpgradeAction = "replace"
	UpgradeCurrent    UpgradeAction = "current"
	UpgradeEdited     UpgradeAction = "edited"
	UpgradeDropped    UpgradeAction = "dropped"
	UpgradeUnwritable UpgradeAction = "unwritable"
)

// UpgradeItem is the plan for one added helper.
type UpgradeItem struct {
	Name   string
	Dir    string
	Layer  config.DataLayer
	Action UpgradeAction
	Entry  RemoteScriptEntry // what the sources offer now; zero when dropped
}

func say(w io.Writer, format string, args ...any) {
	if w != nil {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// scriptDirs returns the helper-scripts directories, nearest first, limited to layer when it is set.
func scriptDirs(layer config.DataLayer) []string {
	dirs := config.GetHelperScriptSearchPaths()
	if layer == "" {
		return dirs
	}
	return config.FilterDirsByLayer(dirs, layer)
}

// fetchScript reads the script an entry names and checks that it is a script the index describes.
func fetchScript(ctx context.Context, entry RemoteScriptEntry) ([]byte, error) {
	data, err := entry.Source.ReadFresh(ctx, entry.Path)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("#!")) {
		return nil, fmt.Errorf("%w: %s has no #! line", ErrNotScript, entry.Path)
	}
	if hashHex(data) != entry.SHA256 {
		return nil, fmt.Errorf("%w: %s", ErrHashMismatch, entry.Path)
	}
	return data, nil
}

// shadowedBy returns the copy of name in a directory nearer than dir, or "".
func shadowedBy(dirs []string, dir, name string) string {
	for _, d := range dirs {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return ""
		}
		if candidate := filepath.Join(d, name); utils.FileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func noteShadow(w io.Writer, dirs []string, dir, name string) {
	if near := shadowedBy(dirs, dir, name); near != "" {
		say(w, "Note: %s is hidden by %s", name, near)
	}
}

// Add puts each named helper in a helper-scripts directory and records it.
//   - layer picks the directory. Empty takes the furthest writable one.
//   - A name already added is refused, and so is a file there that add did not install. force replaces either.
//   - A failure on one name does not stop the others. The errors are joined.
func Add(ctx context.Context, names []string, layer config.DataLayer, force bool, w io.Writer) error {
	offers, err := RefreshRemoteMetadata(ctx, false, w)
	if err != nil {
		return fmt.Errorf("failed to read helper indexes: %w", err)
	}
	var dir string
	if layer == "" {
		dir, err = config.GetWritableHelperScriptsDir()
	} else {
		dir, err = config.GetWritableHelperScriptsDirIn(layer)
	}
	if err != nil {
		return err
	}
	if err := utils.MkdirAllShared(dir); err != nil {
		return fmt.Errorf("failed to create helper scripts directory: %w", err)
	}
	say(w, "Adding to %s (%s)", dir, config.ClassifyDataDir(dir))

	dirs := config.GetHelperScriptSearchPaths()
	var errs []error
	for _, name := range names {
		entry, ok := offers[name]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: %w", name, ErrNotOffered))
			continue
		}
		if err := addOne(ctx, name, entry, dir, force); err != nil {
			say(w, "WARN: %s: %v", name, err)
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		say(w, "Added %s", name)
		noteShadow(w, dirs, dir, name)
	}
	return errors.Join(errs...)
}

func addOne(ctx context.Context, name string, entry RemoteScriptEntry, dir string, force bool) error {
	rec, err := ReadInstalled(dir)
	if err != nil {
		return err
	}
	_, added := rec.Lookup(dir, name)
	if added && !force {
		return fmt.Errorf("%w in %s; use upgrade", ErrAlreadyAdded, dir)
	}
	dest := filepath.Join(dir, name)
	local, readErr := os.ReadFile(dest)
	if readErr != nil || hashHex(local) != entry.SHA256 {
		if readErr == nil && !added && !force {
			return fmt.Errorf("%w: %s; use --force to replace it", ErrUnrecorded, dest)
		}
		data, err := fetchScript(ctx, entry)
		if err != nil {
			return err
		}
		if err := writeExecutable(dest, data); err != nil {
			return err
		}
	}
	return UpdateInstalled(dir, func(r *Installed) error {
		r.Helpers[name] = InstalledEntry{Source: entry.Source.Name, SHA256: entry.SHA256}
		return nil
	})
}

// PlanUpgrade says what an upgrade would do with each added helper, nearest layer first.
//   - names limits it to those helpers. Empty covers every added helper.
//   - layer limits it to the directories of one layer.
//   - A helper is replaced only when the sources offer a different hash and the file is as it was added.
func PlanUpgrade(ctx context.Context, names []string, layer config.DataLayer, w io.Writer) ([]UpgradeItem, error) {
	offers, err := RefreshRemoteMetadata(ctx, false, w)
	if err != nil {
		return nil, fmt.Errorf("failed to read helper indexes: %w", err)
	}
	return planUpgrade(offers, scriptDirs(layer), names)
}

func planUpgrade(offers map[string]RemoteScriptEntry, dirs, names []string) ([]UpgradeItem, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	found := map[string]bool{}
	var items []UpgradeItem
	for _, dir := range dirs {
		rec, err := ReadInstalled(dir)
		if err != nil {
			return nil, err
		}
		sorted := make([]string, 0, len(rec.Helpers))
		for name := range rec.Helpers {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		for _, name := range sorted {
			recorded, ok := rec.Lookup(dir, name)
			if !ok || (len(want) > 0 && !want[name]) {
				continue
			}
			found[name] = true
			item := UpgradeItem{Name: name, Dir: dir, Layer: config.ClassifyDataDir(dir)}
			offer, offered := offers[name]
			local, _ := os.ReadFile(filepath.Join(dir, name))
			switch {
			case !offered:
				item.Action = UpgradeDropped
			case hashHex(local) != recorded.SHA256:
				item.Action = UpgradeEdited
			case offer.SHA256 == recorded.SHA256:
				item.Action = UpgradeCurrent
			case !utils.CanWriteToDir(dir):
				item.Action = UpgradeUnwritable
				item.Entry = offer
			default:
				item.Action = UpgradeReplace
				item.Entry = offer
			}
			items = append(items, item)
		}
	}
	var errs []error
	for _, n := range names {
		if !found[n] {
			errs = append(errs, fmt.Errorf("%s: %w", n, ErrNotAdded))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return items, nil
}

// ApplyUpgrade replaces the helpers a plan marks UpgradeReplace.
//   - A failure on one helper does not stop the others. The errors are joined.
func ApplyUpgrade(ctx context.Context, items []UpgradeItem, w io.Writer) error {
	dirs := config.GetHelperScriptSearchPaths()
	var errs []error
	for _, item := range items {
		if item.Action != UpgradeReplace {
			continue
		}
		if err := upgradeOne(ctx, item); err != nil {
			say(w, "WARN: %s in %s: %v", item.Name, item.Dir, err)
			errs = append(errs, fmt.Errorf("%s: %w", item.Name, err))
			continue
		}
		say(w, "Upgraded %s in %s", item.Name, item.Dir)
		noteShadow(w, dirs, item.Dir, item.Name)
	}
	return errors.Join(errs...)
}

func upgradeOne(ctx context.Context, item UpgradeItem) error {
	data, err := fetchScript(ctx, item.Entry)
	if err != nil {
		return err
	}
	if err := writeExecutable(filepath.Join(item.Dir, item.Name), data); err != nil {
		return err
	}
	return UpdateInstalled(item.Dir, func(r *Installed) error {
		r.Helpers[item.Name] = InstalledEntry{Source: item.Entry.Source.Name, SHA256: item.Entry.SHA256}
		return nil
	})
}

// Remove deletes each named helper that add installed.
//   - layer limits it to one layer. Empty takes the nearest added copy.
//   - A file add did not install is left, and a helper with a run in progress is refused.
//   - A failure on one name does not stop the others. The errors are joined.
func Remove(names []string, layer config.DataLayer, w io.Writer) error {
	dirs := scriptDirs(layer)
	var errs []error
	for _, name := range names {
		if runs, _ := RunningHelpers(name); len(runs) > 0 {
			errs = append(errs, fmt.Errorf("%s: %w", name, ErrInUse))
			continue
		}
		path, uncovered, err := removeOne(name, dirs)
		if err != nil {
			say(w, "WARN: %s: %v", name, err)
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		say(w, "Removed %s", path)
		if uncovered != "" {
			say(w, "Note: %s now resolves to %s", name, uncovered)
		}
	}
	return errors.Join(errs...)
}

func removeOne(name string, dirs []string) (removed, uncovered string, err error) {
	unrecorded := ""
	for i, dir := range dirs {
		rec, err := ReadInstalled(dir)
		if err != nil {
			return "", "", err
		}
		path := filepath.Join(dir, name)
		if _, added := rec.Lookup(dir, name); !added {
			if unrecorded == "" && utils.FileExists(path) {
				unrecorded = path
			}
			continue
		}
		if err := os.Remove(path); err != nil {
			return "", "", err
		}
		if err := UpdateInstalled(dir, func(r *Installed) error {
			delete(r.Helpers, name)
			return nil
		}); err != nil {
			return "", "", err
		}
		for _, farther := range dirs[i+1:] {
			if candidate := filepath.Join(farther, name); utils.FileExists(candidate) {
				return path, candidate, nil
			}
		}
		return path, "", nil
	}
	if unrecorded != "" {
		return "", "", fmt.Errorf("%w: %s is left in place", ErrUnrecorded, unrecorded)
	}
	return "", "", ErrNotAdded
}
