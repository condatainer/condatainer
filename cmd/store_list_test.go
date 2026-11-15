package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/store"
)

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
