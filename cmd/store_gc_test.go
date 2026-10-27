package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/config"
)

// No scope means every writable root, which store.GC allows for a report and
// refuses for an apply.
func TestStoreGCDirsIsEmptyWithoutAScope(t *testing.T) {
	dirs, err := storeGCDirs("")
	if err != nil {
		t.Fatal(err)
	}
	if dirs != nil {
		t.Fatalf("dirs = %v, want nil so GC walks every writable root", dirs)
	}
}

// A layer nobody can spell is an error, not an empty selection.
func TestStoreGCDirsRejectsAnUnknownLayer(t *testing.T) {
	_, err := storeGCDirs("nowhere")
	if err == nil {
		t.Fatal("an unknown layer was accepted")
	}
}

// A layer narrows the roots to the ones that belong to it.
func TestStoreGCDirsKeepsOnlyTheLayer(t *testing.T) {
	dirs, err := storeGCDirs("u")
	if err != nil {
		t.Skipf("no user-layer image directory on this host: %v", err)
	}
	for _, dir := range dirs {
		if config.ClassifyDataDir(dir) != config.LayerUser {
			t.Errorf("%s is not in the user layer", dir)
		}
	}
}
