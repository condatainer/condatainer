package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
)

// A collection that deleted writes removed and the bytes freed; a report writes
// would_remove. Ages and the grace are in seconds.
func TestWriteCollection(t *testing.T) {
	collected := &store.GCReport{
		Grace: 30 * 24 * time.Hour, Reclaimable: 1024,
		Collectable: []store.Collectable{{
			Path: "/images/store/old.sqf", Root: "/images", Name: "old/1", Size: 1024,
			Age: 2 * time.Hour, Basis: store.BasisCtime,
			Identity: meta.KeyRef{Scheme: "script-identity-v1", SHA256: "9f2c"},
		}},
		Retained: []store.Retained{{Path: "/images/store/young.sqf", Reason: "mtime is 1h old"}},
	}
	for _, applied := range []bool{false, true} {
		collected.Applied = applied
		var out bytes.Buffer
		if err := writeCollection(&out, collected); err != nil {
			t.Fatal(err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		key, sizeKey := "would_remove", "reclaimable"
		if applied {
			key, sizeKey = "removed", "reclaimed"
		}
		if _, ok := doc[key]; !ok || string(doc[sizeKey]) != "1024" || string(doc["grace"]) != "2592000" {
			t.Errorf("applied=%v: document = %s", applied, out.String())
		}
		var entries []report.Collected
		if err := json.Unmarshal(doc[key], &entries); err != nil || len(entries) != 1 {
			t.Fatalf("entries = %s, %v", doc[key], err)
		}
		if e := entries[0]; e.Name != "old/1" || e.Age != 7200 || e.Basis != "ctime" || e.Size != 1024 || e.Layout != store.LayoutStored {
			t.Errorf("entry = %+v", e)
		}
	}
}

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
