package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
)

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
