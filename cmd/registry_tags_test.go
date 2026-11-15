package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
)

// Each tag keeps its spelling and says what it is; one this tool did not write
// has no name or key.
func TestTagsDocReadsEachTag(t *testing.T) {
	doc := tagsDoc("ghcr.io/lab/cnt", []string{"star--2.7.11b__eq-e90d3b7714cf__amd64", "latest"})
	if doc.Repository != "ghcr.io/lab/cnt" || len(doc.Tags) != 2 {
		t.Fatalf("doc = %+v", doc)
	}
	if got := doc.Tags[0]; got.Kind != registry.TagEquiv || got.Name != "star/2.7.11b" || got.Key != "e90d3b7714cf" || got.Arch != "amd64" {
		t.Errorf("equiv tag = %+v", got)
	}
	if got := doc.Tags[1]; got != (report.Tag{Tag: "latest", Kind: registry.TagOther}) {
		t.Errorf("other tag = %+v", got)
	}
}
