package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

var (
	ErrNotOffered   = errors.New("helper: not offered by any source")
	ErrAlreadyAdded = errors.New("helper: already added")
	ErrNotAdded     = errors.New("helper: not added")
	ErrUnrecorded   = errors.New("helper: file was not installed by add")
	ErrHashMismatch = errors.New("helper: script does not match its index hash")
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
	if err := utils.CheckShebang(data); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNotScript, entry.Path, err)
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
	say(w, "Adding to %s (%s layer)", dir, config.ClassifyDataDir(dir))

	dirs := config.GetHelperScriptSearchPaths()
	var errs []error
	for _, name := range names {
		entry, ok := offers[name]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: %w", name, ErrNotOffered))
			continue
		}
		if err := addOne(ctx, name, entry, dir, force); err != nil {
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
		return fmt.Errorf("%w in %s; `condatainer helper upgrade` brings it up to date, and `--force` replaces it", ErrAlreadyAdded, dir)
	}
	dest := filepath.Join(dir, name)
	local, readErr := os.ReadFile(dest)
	if readErr != nil || hashHex(local) != entry.SHA256 {
		if readErr == nil && !added && !force {
			return fmt.Errorf("%w: %s; `--force` replaces it", ErrUnrecorded, dest)
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
	return PlanUpgradeWith(offers, layer, names)
}

// PlanUpgradeWith is PlanUpgrade against offers already read.
func PlanUpgradeWith(offers map[string]RemoteScriptEntry, layer config.DataLayer, names []string) ([]UpgradeItem, error) {
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
			errs = append(errs, fmt.Errorf("%s in %s: %w", item.Name, item.Dir, err))
			continue
		}
		say(w, "Upgraded %s in %s (%s layer)", item.Name, item.Dir, item.Layer)
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

// RemoveItem is one added helper that remove would delete.
type RemoveItem struct {
	Name     string
	Dir      string
	Path     string
	Uncovers string // the farther copy that serves the name afterwards, or ""
}

// PlanRemove says what remove would delete for each named helper.
//   - layer limits it to one layer. Empty takes the nearest added copy.
//   - A file add did not install is left.
//   - A name that cannot be removed is an error, and the others are still planned. The errors are joined.
func PlanRemove(names []string, layer config.DataLayer) ([]RemoveItem, error) {
	dirs := scriptDirs(layer)
	var items []RemoveItem
	var errs []error
	for _, name := range names {
		item, err := findRemovable(name, dirs)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		items = append(items, item)
	}
	return items, errors.Join(errs...)
}

// Remove deletes the helpers a plan names and drops their records.
//   - A failure on one does not stop the others. The errors are joined.
func Remove(items []RemoveItem, w io.Writer) error {
	var errs []error
	for _, item := range items {
		if err := removeItem(item); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", item.Name, err))
			continue
		}
		say(w, "Removed %s%s", item.Path, config.LayerNote(item.Path))
		if item.Uncovers != "" {
			say(w, "Note: %s now resolves to %s", item.Name, item.Uncovers)
		}
	}
	return errors.Join(errs...)
}

func findRemovable(name string, dirs []string) (RemoveItem, error) {
	unrecorded := ""
	for i, dir := range dirs {
		rec, err := ReadInstalled(dir)
		if err != nil {
			return RemoveItem{}, err
		}
		path := filepath.Join(dir, name)
		if _, added := rec.Lookup(dir, name); !added {
			if unrecorded == "" && utils.FileExists(path) {
				unrecorded = path
			}
			continue
		}
		item := RemoveItem{Name: name, Dir: dir, Path: path}
		for _, farther := range dirs[i+1:] {
			if candidate := filepath.Join(farther, name); utils.FileExists(candidate) {
				item.Uncovers = candidate
				break
			}
		}
		return item, nil
	}
	if unrecorded != "" {
		return RemoveItem{}, fmt.Errorf("%w: %s is left in place", ErrUnrecorded, unrecorded)
	}
	return RemoveItem{}, ErrNotAdded
}

func removeItem(item RemoveItem) error {
	if err := os.Remove(item.Path); err != nil {
		return err
	}
	return UpdateInstalled(item.Dir, func(r *Installed) error {
		delete(r.Helpers, item.Name)
		return nil
	})
}

// ListedScript is one script in a helper-scripts directory.
type ListedScript struct {
	Name        string
	Description string
	Added       bool   // recorded by add
	HiddenBy    string // the nearer copy that hides it, or ""
}

// ListedDir is the scripts of one helper-scripts directory.
type ListedDir struct {
	Dir     string
	Layer   config.DataLayer
	Scripts []ListedScript
}

// ListScripts reads the scripts of each directory, nearest first, sorted by name.
//   - A hidden file or a subdirectory is not a script.
//   - A directory that cannot be read is left out.
func ListScripts() ([]ListedDir, error) {
	dirs := config.GetHelperScriptSearchPaths()
	var out []ListedDir
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		rec, err := ReadInstalled(dir)
		if err != nil {
			return nil, err
		}
		group := ListedDir{Dir: dir, Layer: config.ClassifyDataDir(dir)}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") {
				continue
			}
			_, added := rec.Lookup(dir, name)
			group.Scripts = append(group.Scripts, ListedScript{
				Name:        name,
				Description: ScriptDescription(filepath.Join(dir, name)),
				Added:       added,
				HiddenBy:    shadowedBy(dirs, dir, name),
			})
		}
		sort.Slice(group.Scripts, func(i, j int) bool { return group.Scripts[i].Name < group.Scripts[j].Name })
		out = append(out, group)
	}
	return out, nil
}
