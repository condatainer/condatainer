package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/condatainer/condatainer/internal/config"
)

// fileBookmark is one starred filesystem path shown in the dashboard's file
// tree, persisted server-side (not in browser localStorage) so it's shared
// across browsers/devices for the same user.
type fileBookmark struct {
	Path    string    `json:"path"`
	Label   string    `json:"label,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// fileBookmarksStore returns the jsonStore backing ~/.local/state/condatainer/file-bookmarks.json.
func fileBookmarksStore() (*jsonStore[[]fileBookmark], error) {
	path := config.GetFileBookmarksPath()
	if path == "" {
		return nil, fmt.Errorf("cannot determine file-bookmarks path")
	}
	return newJSONStore[[]fileBookmark](path), nil
}

// listFileBookmarks returns all file bookmarks (oldest first). Returns an
// empty slice, not an error, if the file-bookmarks file does not exist yet.
func listFileBookmarks() ([]fileBookmark, error) {
	store, err := fileBookmarksStore()
	if err != nil {
		return nil, err
	}
	list, err := store.Load()
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []fileBookmark{}
	}
	return list, nil
}

// addFileBookmark stars path, assigning label (defaults to filepath.Base(path) if empty).
//   - Re-adding an already-bookmarked path updates its label rather than duplicating the entry. path must currently exist on disk.
//   - Returns the full updated list.
func addFileBookmark(path, label string) ([]fileBookmark, error) {
	store, err := fileBookmarksStore()
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(os.ExpandEnv(path))
	abs, err := filepath.Abs(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("path does not exist: %w", err)
	}
	if label == "" {
		label = filepath.Base(abs)
	}

	return store.Update(func(list *[]fileBookmark) error {
		if *list == nil {
			*list = []fileBookmark{}
		}
		for i := range *list {
			if (*list)[i].Path == abs {
				(*list)[i].Label = label
				return nil
			}
		}
		*list = append(*list, fileBookmark{Path: abs, Label: label, AddedAt: time.Now()})
		return nil
	})
}

// orderBookmarks returns list in the order of paths.
//   - A path not in list, or repeated, is ignored.
//   - A bookmark not named in paths follows the named ones in its current order, so a bookmark added meanwhile is kept.
func orderBookmarks(list []fileBookmark, paths []string) []fileBookmark {
	byPath := make(map[string]fileBookmark, len(list))
	for _, b := range list {
		byPath[b.Path] = b
	}
	out := make([]fileBookmark, 0, len(list))
	placed := make(map[string]bool, len(list))
	for _, p := range paths {
		if b, ok := byPath[p]; ok && !placed[p] {
			out = append(out, b)
			placed[p] = true
		}
	}
	for _, b := range list {
		if !placed[b.Path] {
			out = append(out, b)
		}
	}
	return out
}

// reorderFileBookmarks stores the bookmarks in the order of paths and
// returns the full updated list.
func reorderFileBookmarks(paths []string) ([]fileBookmark, error) {
	store, err := fileBookmarksStore()
	if err != nil {
		return nil, err
	}
	return store.Update(func(list *[]fileBookmark) error {
		*list = orderBookmarks(*list, paths)
		return nil
	})
}

// removeFileBookmark unstars path (a no-op if it was not bookmarked).
// Returns the full updated list.
func removeFileBookmark(path string) ([]fileBookmark, error) {
	store, err := fileBookmarksStore()
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(os.ExpandEnv(path))
	abs, err := filepath.Abs(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	return store.Update(func(list *[]fileBookmark) error {
		if *list == nil {
			*list = []fileBookmark{}
		}
		filtered := (*list)[:0]
		for _, b := range *list {
			if b.Path != abs {
				filtered = append(filtered, b)
			}
		}
		*list = filtered
		return nil
	})
}
