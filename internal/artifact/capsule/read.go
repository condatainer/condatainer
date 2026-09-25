package capsule

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one artifact in an embedded closure.
type Entry struct {
	// Dir is the entry directory name, <name>@<short identity>.
	Dir string
	// Name is the artifact's name, with its slashes restored.
	Name string
	// Identity is the truncated identity from the directory name. It addresses
	// the entry; the full digest is in manifest.json and verified from sources.
	Identity string
	// Files are the entry's file names, sorted.
	Files []string
}

// Entries lists a capsule directory. A missing capsule is not an error: only
// data has dependencies, so most artifacts have none.
func Entries(dir string) ([]Entry, error) {
	found, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list capsule %s: %w", dir, err)
	}

	var out []Entry
	for _, node := range found {
		if !node.IsDir() {
			return nil, fmt.Errorf("%w: %s is not an entry directory", ErrInvalid, node.Name())
		}
		entry, err := readEntry(dir, node.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// readEntry parses one entry directory name and lists what it holds.
func readEntry(dir, name string) (Entry, error) {
	artifact, identity, ok := strings.Cut(name, "@")
	if !ok || artifact == "" || identity == "" {
		return Entry{}, fmt.Errorf("%w: %q is not <name>@<identity>", ErrInvalid, name)
	}
	entry := Entry{
		Dir:      name,
		Name:     strings.ReplaceAll(artifact, "--", "/"),
		Identity: identity,
	}

	files, err := os.ReadDir(filepath.Join(dir, name))
	if err != nil {
		return Entry{}, fmt.Errorf("cannot list capsule entry %s: %w", name, err)
	}
	for _, file := range files {
		if file.IsDir() {
			return Entry{}, fmt.Errorf("%w: %s nests a directory; entries are flat", ErrInvalid, name)
		}
		entry.Files = append(entry.Files, file.Name())
	}
	sort.Strings(entry.Files)
	return entry, nil
}
