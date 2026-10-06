package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"go.yaml.in/yaml/v3"
)

// ConfigFilename is the name of the config file
const ConfigFilename = "config"

// ConfigType is the type of config file
const ConfigType = "yaml"

// configLayers holds the loaded config files in priority order (user, extra-root, app-root).
var (
	configLayers []*Layer
	layersMu     sync.RWMutex
)

// loadedLayers returns the loaded config files. A reload replaces the slice, never edits it.
func loadedLayers() []*Layer {
	layersMu.RLock()
	defer layersMu.RUnlock()
	return configLayers
}

// GetConfigLayers returns the loaded config files in priority order.
func GetConfigLayers() []*Layer { return loadedLayers() }

// LoadLayers reads every existing config file and sets the defaults. A scalar key
// is won by the highest-priority layer that sets it; array keys merge across all
// layers, channels excepted.
func LoadLayers() error {
	recordStamps()
	type configSource struct{ path, label string }
	var sources []configSource
	if userPath, err := GetUserConfigPath(); err == nil {
		sources = append(sources, configSource{userPath, "user"})
	}
	if extraRoot := GetExtraRootDir(); extraRoot != "" {
		sources = append(sources, configSource{filepath.Join(extraRoot, ConfigFilename+"."+ConfigType), "extra-root"})
	}
	if rootPath := GetRootConfigPath(); rootPath != "" {
		sources = append(sources, configSource{rootPath, "app-root"})
	}

	// seenPaths prevents loading the same file twice (e.g. CNT_EXTRA_ROOT == CNT_ROOT).
	var layers []*Layer
	seenPaths := make(map[string]bool)
	for _, src := range sources {
		if !fileExists(src.path) || seenPaths[src.path] {
			continue
		}
		seenPaths[src.path] = true
		layer, err := readLayer(src.path, src.label)
		if err != nil {
			return err
		}
		layers = append(layers, layer)
	}
	feed := make([]settings.Layer, len(layers))
	for i, l := range layers {
		feed[i] = l
	}
	layersMu.Lock()
	configLayers = layers
	layersMu.Unlock()
	settings.SetLayers(feed)
	return nil
}

// GetUserConfigPath returns the path to the user config file
func GetUserConfigPath() (string, error) {
	dir := GetUserConfigDir()
	if dir == "" {
		return "", errors.New("no home directory for the user config")
	}
	return filepath.Join(dir, ConfigFilename+"."+ConfigType), nil
}

// GetRootConfigPath returns the root config path (CNT_ROOT or standalone layout).
// Returns empty string if the root dir is not set.
func GetRootConfigPath() string {
	if rootDir := GetRootDir(); rootDir != "" {
		return filepath.Join(rootDir, ConfigFilename+"."+ConfigType)
	}
	return ""
}

// GetExtraRootConfigPath returns the extra-root config path ($CNT_EXTRA_ROOT/config.yaml).
// Returns empty string if CNT_EXTRA_ROOT is not set.
func GetExtraRootConfigPath() string {
	if extraRoot := GetExtraRootDir(); extraRoot != "" {
		return filepath.Join(extraRoot, ConfigFilename+"."+ConfigType)
	}
	return ""
}

// ConfigSearchPath represents a config file location with metadata
type ConfigSearchPath struct {
	Path   string // Full path to config file
	Type   string // Type: "user", "extra-root", "app-root"
	Exists bool   // Whether the file exists
	InUse  bool   // Whether this is the active config file
}

// fileExists checks if a path exists and is a regular file (not a directory)
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// GetLoadedConfigPaths returns the file paths of all config files actually loaded by
// LoadLayers, in priority order. These are the files that actively contribute to the
// effective configuration (via scalar precedence + array merging).
func GetLoadedConfigPaths() []string {
	paths := make([]string, 0, len(loadedLayers()))
	for _, l := range loadedLayers() {
		paths = append(paths, l.Path)
	}
	return paths
}

// GetConfigSearchPaths returns all config search paths in priority order.
//   - This matches the order used by LoadLayers.
//   - InUse is true for every config file that was loaded (all contribute via merging), not just the primary.
//   - Duplicate paths (e.g. CNT_EXTRA_ROOT == CNT_ROOT) are skipped.
func GetConfigSearchPaths() []ConfigSearchPath {
	// Build loaded-path set for InUse: all loaded layers contribute, not just primary.
	loadedPaths := make(map[string]bool)
	for _, p := range GetLoadedConfigPaths() {
		loadedPaths[p] = true
	}

	var paths []ConfigSearchPath
	seenPaths := make(map[string]bool)

	add := func(path, typ string) {
		if path == "" || seenPaths[path] {
			return
		}
		seenPaths[path] = true
		paths = append(paths, ConfigSearchPath{
			Path:   path,
			Type:   typ,
			Exists: fileExists(path),
			InUse:  loadedPaths[path],
		})
	}

	// Priority order matches LoadLayers: user > extra-root > app-root
	if userPath, err := GetUserConfigPath(); err == nil {
		add(userPath, "user")
	}
	if extraRoot := GetExtraRootDir(); extraRoot != "" {
		add(filepath.Join(extraRoot, ConfigFilename+"."+ConfigType), "extra-root")
	}
	if rootPath := GetRootConfigPath(); rootPath != "" {
		add(rootPath, "app-root")
	}

	return paths
}

// NormalizeConfigLayer expands a config layer shorthand to its full name.
//   - Returns the full name ("user", "app-root", "extra-root") or the input unchanged.
//   - "root" and "r" are accepted as aliases for "app-root".
func NormalizeConfigLayer(layer string) string {
	switch layer {
	case "u":
		return "user"
	case "r", "root":
		return "app-root"
	case "e":
		return "extra-root"
	default:
		return layer
	}
}

// GetConfigPathByLayer returns the config path for the specified config layer.
// Supported layers: "user"/"u", "app-root"/"root"/"r", "extra-root"/"e"
func GetConfigPathByLayer(layer string) (string, error) {
	switch layer {
	case "user", "u":
		return GetUserConfigPath()
	case "app-root", "root", "r":
		if path := GetRootConfigPath(); path != "" {
			return path, nil
		}
		return "", fmt.Errorf("app-root dir not available (not a standalone layout and CNT_ROOT not set)")
	case "extra-root", "e":
		if path := GetExtraRootConfigPath(); path != "" {
			return path, nil
		}
		return "", fmt.Errorf("CNT_EXTRA_ROOT is not set")
	default:
		return "", fmt.Errorf("invalid layer '%s': use 'user' (u), 'extra-root' (e), or 'app-root' (r)", layer)
	}
}

// inferConfigLayer returns "app-root", "extra-root", or "user" by comparing path
// against known config file paths. Returns "user" for any unrecognised path.
func inferConfigLayer(path string) string {
	if p := GetExtraRootConfigPath(); p != "" && path == p {
		return "extra-root"
	}
	if p := GetRootConfigPath(); p != "" && path == p {
		return "app-root"
	}
	return "user"
}

// ResolveWritableConfigPath determines where a config-mutating command should write.
//   - A non-empty layer is resolved and checked for writability.
//   - With no layer, the active config file is used when writable. Otherwise it falls back to the user config path, as `config init` does.
//   - Writability is checked on the file itself, not just its directory. A read-only file in a writable directory, such as a frozen shared config, is not a valid target.
func ResolveWritableConfigPath(layer string) (path, layerType string, err error) {
	path, layerType, _, err = resolveWritableConfigPath(layer)
	return path, layerType, err
}

// ResolveWritableConfigPathVerbose is ResolveWritableConfigPath plus the layer it
// fell back from. fellBackFrom is empty unless an active config existed but was
// read-only, forcing the write down to the user layer — callers report that, since
// the change then applies only to the current user.
func ResolveWritableConfigPathVerbose(layer string) (path, layerType, fellBackFrom string, err error) {
	return resolveWritableConfigPath(layer)
}

func resolveWritableConfigPath(layer string) (path, layerType, fellBackFrom string, err error) {
	if layer != "" {
		path, err = GetConfigPathByLayer(layer)
		if err != nil {
			return "", "", "", err
		}
		layerType = NormalizeConfigLayer(layer)
		if !utils.CanWriteToFile(path) {
			return "", "", "", fmt.Errorf(
				"config layer '%s' is read-only: %s\nuse a different layer or run with appropriate permissions",
				layerType, path,
			)
		}
		return path, layerType, "", nil
	}

	// Auto-detect: active config file when writable, else user config
	if layers := loadedLayers(); len(layers) > 0 {
		active := layers[0].Path
		if utils.CanWriteToFile(active) {
			return active, inferConfigLayer(active), "", nil
		}
		fellBackFrom = inferConfigLayer(active)
	}
	path, err = GetUserConfigPath()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to determine user config path: %w", err)
	}
	if fellBackFrom == "user" {
		// The user's own config is unwritable; falling back to itself would loop.
		return "", "", "", fmt.Errorf(
			"user config is read-only: %s\nfix its permissions to change settings", path,
		)
	}
	return path, "user", fellBackFrom, nil
}

// ResolveReadableConfigPath resolves a config layer name to a config file path for reading.
// Unlike ResolveWritableConfigPath it does not check write permissions.
func ResolveReadableConfigPath(layer string) (path, layerType string, err error) {
	if layer == "" {
		path, err = GetUserConfigPath()
		if err != nil {
			return "", "", fmt.Errorf("failed to determine user config path: %w", err)
		}
		return path, "user", nil
	}
	path, err = GetConfigPathByLayer(layer)
	if err != nil {
		return "", "", err
	}
	return path, NormalizeConfigLayer(layer), nil
}

// ReadConfigSliceKey reads a string-slice key from a config file.
// Returns nil if the file doesn't exist or the key is not set.
func ReadConfigSliceKey(configPath, key string) []string {
	if configPath == "" {
		return nil
	}
	l, err := readLayer(configPath, "")
	if err != nil {
		return nil
	}
	return l.GetStringSlice(key)
}

// UpdateConfigKey sets key to value in the config file at path, creating the directory and file if needed.
func UpdateConfigKey(path, key string, value any) error {
	return editLayerFile(path, func(root *yaml.Node) error { return setKey(root, key, value) })
}

// ReadConfigAllKeys returns all keys explicitly set in the config file at path.
func ReadConfigAllKeys(path string) []string {
	l, err := readLayer(path, "")
	if err != nil {
		return nil
	}
	return l.Keys()
}

// ReadConfigKey reads a single key from a config file.
// Returns "" if the file doesn't exist, can't be read, or the key is not set.
func ReadConfigKey(configPath, key string) string {
	if configPath == "" {
		return ""
	}
	l, err := readLayer(configPath, "")
	if err != nil {
		return ""
	}
	return l.GetString(key)
}

// SaveDetectedConfigTo writes only the detected values to path, so no default values bleed in.
// A key already set in one of lowerLayers is skipped.
func SaveDetectedConfigTo(path string, detected map[string]string, lowerLayers []*Layer) error {
	alreadySet := func(key string) bool {
		for _, l := range lowerLayers {
			if l.InConfig(key) {
				return true
			}
		}
		return false
	}
	keys := make([]string, 0, len(detected))
	for key := range detected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return editLayerFile(path, func(root *yaml.Node) error {
		root.Content = nil
		for _, key := range keys {
			if alreadySet(key) {
				continue
			}
			if err := setKey(root, key, detected[key]); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetConfigKey sets a single key in the config file at path, creating it if needed.
//   - Existing keys are preserved.
//   - No-op if the file already has the key set to the same value.
func SetConfigKey(path, key, value string) error {
	if l, err := readLayer(path, ""); err == nil && l.GetString(key) == value {
		return nil
	}
	return UpdateConfigKey(path, key, value)
}

// RenameConfigKey moves the value of old to new in the config file at path, converting it with migrate when it is not nil.
//   - A key renamed within its own section is renamed where it stands, so order and comments stay.
//   - When new is already set the old value is dropped, since the new key wins.
func RenameConfigKey(path, old, new string, migrate func(string) (string, error)) error {
	return editLayerFile(path, func(root *yaml.Node) error {
		node := lookup(root, splitKey(old))
		if node == nil {
			return nil
		}
		if lookup(root, splitKey(new)) != nil {
			deleteKey(root, splitKey(old))
			return nil
		}
		if migrate != nil && node.Kind == yaml.ScalarNode {
			migrated, err := migrate(node.Value)
			if err != nil {
				return fmt.Errorf("%s: %w", old, err)
			}
			if migrated != node.Value {
				n, err := newValueNode(migrated)
				if err != nil {
					return err
				}
				n.LineComment = node.LineComment
				*node = *n
			}
		}
		if renameInPlace(root, splitKey(old), splitKey(new)) {
			return nil
		}
		keep := *node
		deleteKey(root, splitKey(old))
		return setKeyNode(root, new, &keep)
	})
}

// renameInPlace renames the last segment of a nested key whose section does not change.
func renameInPlace(root *yaml.Node, oldParts, newParts []string) bool {
	if len(oldParts) != len(newParts) || len(oldParts) == 0 {
		return false
	}
	m := root
	for i, p := range oldParts[:len(oldParts)-1] {
		if p != newParts[i] {
			return false
		}
		idx := childIndex(m, p)
		if idx < 0 || deref(m.Content[idx]).Kind != yaml.MappingNode {
			return false
		}
		m = deref(m.Content[idx])
	}
	idx := childIndex(m, oldParts[len(oldParts)-1])
	if idx < 0 {
		return false
	}
	m.Content[idx-1].Value = newParts[len(newParts)-1]
	return true
}

// DeleteConfigKey removes a key from the config file at path, and any parent mapping it leaves empty.
// A missing file is not an error.
func DeleteConfigKey(path, key string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return editLayerFile(path, func(root *yaml.Node) error {
		deleteKey(root, splitKey(key))
		return nil
	})
}

// LoadSources resolves the recipe sources from the loaded layers.
func LoadSources() {
	// Recipe collections, in order; earlier entries shadow later ones.
	Global.Sources = layerSources()
}
