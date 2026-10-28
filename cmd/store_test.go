package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
)

func storeCandidate(t *testing.T, root string) store.Candidate {
	t.Helper()
	path := filepath.Join(root, "store", "star--2.7.11b@9f2c1ab4d3e8.sqf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("sqf"), 0o644); err != nil {
		t.Fatal(err)
	}
	return store.Candidate{
		Name: "star/2.7.11b", Path: path, Root: root, Layout: store.LayoutStored, Size: 3,
		Identity: meta.KeyRef{Scheme: "script-identity-v1", SHA256: "9f2c1ab4d3e8"},
		Equiv:    meta.KeyRef{Scheme: "script-equiv-v1", SHA256: "e90d"},
	}
}

// A listing is one record per entry with its layout and keys, and an entry that
// could not be trusted is a problem.
func TestStoreListDoc(t *testing.T) {
	c := storeCandidate(t, t.TempDir())
	doc := storeListDoc(store.Report{
		Candidates: []store.Candidate{c},
		Issues:     []store.Issue{{Path: "/images/store/bad.sqf", Error: "keys do not regenerate"}},
	})
	if len(doc.Overlays) != 1 || len(doc.Problems) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	got := doc.Overlays[0]
	if got.Name != "star/2.7.11b" || got.Layout != store.LayoutStored || got.Path != c.Path || got.Size != 3 ||
		got.Keys == nil || got.Keys.Identity != c.Identity || got.Keys.Equiv != c.Equiv {
		t.Errorf("record = %+v", got)
	}
	if p := doc.Problems[0]; p.Name != "/images/store/bad.sqf" || p.Message != "keys do not regenerate" {
		t.Errorf("problem = %+v", p)
	}
}

// A promotion describes the build where it is now, and the demoted build where
// it went. A build that already answered to the name is skipped.
func TestPromotionDoc(t *testing.T) {
	root := t.TempDir()
	promoted := storeCandidate(t, root)
	flat := filepath.Join(root, "star--2.7.11b.sqf")
	if err := os.WriteFile(flat, []byte("sqf"), 0o644); err != nil {
		t.Fatal(err)
	}
	promoted.Path, promoted.Layout = flat, store.LayoutFlat
	demoted := store.Candidate{Name: "star/2.7.11b", Path: flat, Root: root, Layout: store.LayoutFlat,
		Identity: meta.KeyRef{Scheme: "script-identity-v1", SHA256: "0123456789ab"}}

	doc := promotionDoc(store.Promotion{Name: "star/2.7.11b", Promoted: promoted, Demoted: demoted,
		DemotedTo: filepath.Join(root, "store", "star--2.7.11b@0123456789ab.sqf"), Shadows: "/far/star--2.7.11b.sqf"})
	if doc.Outcome != report.OutcomeDone || doc.Overlay.Layout != store.LayoutFlat || doc.Overlay.Path != flat ||
		doc.Shadows != "/far/star--2.7.11b.sqf" || doc.AlreadyCurrent {
		t.Errorf("promotion = %+v", doc)
	}
	if doc.Demoted == nil || doc.Demoted.Layout != store.LayoutStored || filepath.Base(doc.Demoted.Path) != "star--2.7.11b@0123456789ab.sqf" {
		t.Errorf("demoted = %+v", doc.Demoted)
	}

	if doc := promotionDoc(store.Promotion{Promoted: promoted, AlreadyFlat: true}); doc.Outcome != report.OutcomeSkipped ||
		!doc.AlreadyCurrent || doc.Demoted != nil {
		t.Errorf("already current = %+v", doc)
	}
}

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
