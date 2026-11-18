package helperhistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/project/lock"
)

func testRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, lock.DirName), 0o775); err != nil {
		t.Fatal(err)
	}
	return root
}

// RecordUsed is a no-op with no standing project — nothing to record for and
// no error either.
func TestRecordUsedNoopWithNoRoot(t *testing.T) {
	if err := RecordUsed("", "rstudio-server", ".", nil, []string{"build-essential"}, nil); err != nil {
		t.Fatalf("RecordUsed(\"\", ...) = %v, want nil", err)
	}
}

// A fresh combination writes exactly one file.
func TestRecordUsedWritesOneFile(t *testing.T) {
	root := testRoot(t)
	if err := RecordUsed(root, "rstudio-server", ".", nil, []string{"build-essential", "r/4.4.3"}, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files, want 1", len(entries))
	}
}

// The same combination again bumps the existing file's mtime rather than
// writing a second one — no new file, and the timestamp moves forward.
func TestRecordUsedRepeatBumpsMtimeNotANewFile(t *testing.T) {
	root := testRoot(t)
	overlays := []string{"build-essential", "r/4.4.3"}
	if err := RecordUsed(root, "rstudio-server", ".", nil, overlays, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(root), entries[0].Name())
	old, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Force the clock forward so a real bump is distinguishable from a file
	// that was simply left untouched.
	past := old.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}

	// Order shuffled and re-sorted internally, so this still counts as the
	// same combination.
	if err := RecordUsed(root, "rstudio-server", ".", nil, []string{"r/4.4.3", "build-essential"}, nil); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files after a repeat use, want 1", len(entries))
	}
	newInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !newInfo.ModTime().After(past) {
		t.Fatalf("mtime was not bumped: %v", newInfo.ModTime())
	}
}

// A different required or added overlay set at the same helper+location is a
// second, distinct combination — two files, not a replacement.
func TestRecordUsedDifferentOverlaysIsASecondFile(t *testing.T) {
	root := testRoot(t)
	if err := RecordUsed(root, "rstudio-server", ".", []string{"r/4.4.3"}, []string{"extra"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := RecordUsed(root, "rstudio-server", ".", []string{"r/4.5.0"}, []string{"extra"}, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d files, want 2 distinct combinations", len(entries))
	}
}

// ListUsed returns only the matching helper+location, newest mtime first.
func TestListUsedFiltersAndOrdersByRecency(t *testing.T) {
	root := testRoot(t)
	if err := RecordUsed(root, "rstudio-server", ".", nil, []string{"r/4.4.3"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := RecordUsed(root, "jupyterlab", ".", nil, []string{"python/3.12"}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := RecordUsed(root, "rstudio-server", ".", nil, []string{"r/4.5.0"}, nil); err != nil {
		t.Fatal(err)
	}

	used, err := ListUsed(root, "rstudio-server", ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 2 {
		t.Fatalf("got %d combinations, want 2 (jupyterlab's must not appear)", len(used))
	}
	if used[0].Overlays[0] != "r/4.5.0" {
		t.Fatalf("newest combination first: got %v", used[0].Overlays)
	}
}

// ListUsed at a location with nothing recorded is empty, not an error.
func TestListUsedEmptyWithNoHistoryDir(t *testing.T) {
	root := testRoot(t)
	used, err := ListUsed(root, "rstudio-server", ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 0 {
		t.Fatalf("got %d combinations, want 0", len(used))
	}
}

// ListAll groups every combination by helper, then location.
func TestListAllGroupsByHelperThenLocation(t *testing.T) {
	root := testRoot(t)
	if err := RecordUsed(root, "rstudio-server", ".", nil, []string{"r/4.4.3"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := RecordUsed(root, "rstudio-server", "sub", nil, []string{"r/4.5.0"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := RecordUsed(root, "jupyterlab", ".", nil, []string{"python/3.12"}, nil); err != nil {
		t.Fatal(err)
	}

	all, err := ListAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d helpers, want 2", len(all))
	}
	if len(all["rstudio-server"]) != 2 {
		t.Fatalf("got %d locations for rstudio-server, want 2", len(all["rstudio-server"]))
	}
	if len(all["jupyterlab"]["."]) != 1 {
		t.Fatalf("got %d combinations for jupyterlab at \".\", want 1", len(all["jupyterlab"]["."]))
	}
}

// Two runs that differ only in a slot's overlay are two combinations, and the slots are read back.
func TestRecordUsedKeepsSlotsAsPartOfTheCombination(t *testing.T) {
	root := testRoot(t)
	human := map[string]string{"genome": "grch38/genome/gencode/49"}
	mouse := map[string]string{"genome": "grcm39/genome/gencode/37"}
	for _, slots := range []map[string]string{human, mouse, human} {
		if err := RecordUsed(root, "igv", ".", nil, nil, slots); err != nil {
			t.Fatal(err)
		}
	}
	used, err := ListUsed(root, "igv", ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 2 {
		t.Fatalf("got %d combinations, want 2 (the repeat of human only bumps it)", len(used))
	}
	got := map[string]bool{}
	for _, u := range used {
		got[u.Slots["genome"]] = true
	}
	if !got[human["genome"]] || !got[mouse["genome"]] {
		t.Errorf("slots read back = %v", got)
	}
}
