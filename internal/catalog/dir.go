package catalog

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// dirBackend reads a filesystem source directly. There is no index to keep
// honest and nothing to cache: names map to paths structurally, so resolving
// one is a stat, and only enumeration walks the tree.
type dirBackend struct{ src *Source }

func (d *dirBackend) read(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.src.Base, filepath.FromSlash(path)))
}

func (d *dirBackend) readFresh(ctx context.Context, path string) ([]byte, error) {
	return d.read(ctx, path)
}

// entries walks recipes/ and parses each file's headers.
func (d *dirBackend) entries(_ context.Context) (map[string]*Entry, error) {
	root := filepath.Join(d.src.Base, recipesDir)
	out := map[string]*Entry{}

	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || skipName(entry.Name()) {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()

		rec, err := ParseRecipe(recipesDir+"/"+filepath.ToSlash(rel), f)
		if err != nil {
			return err
		}
		out[rec.Name] = &rec.Entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HelperFiles lists the helper scripts of a directory source: the files directly in helpers/.
//   - local is false for a source served over HTTP, which lists its helpers in index/helpers.json.
//   - Subdirectories and names that start with a dot are left out.
//   - A directory source without helpers/ offers none.
func (s *Source) HelperFiles() (files []string, local bool, err error) {
	if isURL(s.Base) {
		return nil, false, nil
	}
	entries, err := os.ReadDir(filepath.Join(s.Base, helpersDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, true, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			files = append(files, helpersDir+"/"+entry.Name())
		}
	}
	return files, true, nil
}

// skipName drops files a collection carries but does not offer: documentation,
// and whatever an editor or the OS left behind.
func skipName(name string) bool {
	switch strings.ToLower(name) {
	case "readme.md", ".gitignore", ".gitkeep", ".ds_store":
		return true
	}
	return strings.HasPrefix(name, ".")
}
