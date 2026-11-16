package server

import (
	"slices"
	"testing"
)

// TestOrderBookmarks checks that the listed paths come first in the given
// order, unknown and repeated paths are ignored, and unlisted bookmarks
// follow in their current order.
func TestOrderBookmarks(t *testing.T) {
	list := []fileBookmark{{Path: "/a"}, {Path: "/b"}, {Path: "/c"}, {Path: "/d"}}
	got := orderBookmarks(list, []string{"/c", "/x", "/a", "/c"})

	var paths []string
	for _, b := range got {
		paths = append(paths, b.Path)
	}
	if want := []string{"/c", "/a", "/b", "/d"}; !slices.Equal(paths, want) {
		t.Fatalf("order = %v, want %v", paths, want)
	}
}
