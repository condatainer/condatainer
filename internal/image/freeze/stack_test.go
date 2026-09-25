package freeze

import (
	"slices"
	"testing"
)

// The snapshot's own whiteouts are carried forward unless the image recreates
// the path or hides a directory above it.
func TestPlanStackedCarriesInheritedWhiteouts(t *testing.T) {
	wh := func(p string) archiveNode { return archiveNode{Path: p, Kind: 'c'} }
	dir := func(p string) archiveNode { return archiveNode{Path: p, Kind: 'd'} }
	snapshot := []archiveNode{
		dir("usr"), dir("usr/bin"), wh("usr/bin/kept"), wh("usr/bin/recreated"),
		dir("gone"), wh("gone/x"),
		dir("opq"), dir("opq/sub"), wh("opq/sub/y"),
		dir("swap"), wh("swap/z"),
		dir(".cnt"),
	}
	entries := []Entry{
		{Path: "usr", Mode: 0o040755}, {Path: "usr/bin", Mode: 0o040755},
		{Path: "usr/bin/recreated", Mode: 0o100644}, // the image brings the file back
		{Path: "opq", Mode: 0o040755},               // replaced wholesale, without sub
		{Path: "swap", Mode: 0o100644},              // a directory replaced by a file
	}
	tr := Translation{
		Pseudo: []string{pseudoWhiteout("gone")}, // the image deleted the directory
		Opaque: []string{"opq"},
	}

	got := planStacked(snapshot, entries, tr).Translation.Pseudo
	want := []string{pseudoWhiteout("gone"), pseudoWhiteout("usr/bin/kept")}
	if !slices.Equal(got, want) {
		t.Errorf("whiteouts = %v, want %v", got, want)
	}
}

// The snapshot's own metadata directory is never a root of the new archive.
func TestPlanStackedLeavesOutTheSnapshotMetadata(t *testing.T) {
	snapshot := []archiveNode{{Path: ".cnt", Kind: 'd'}, {Path: "cnt_env", Kind: 'd'}}
	entries := []Entry{{Path: "opt", Mode: 0o040755}}
	got := planStacked(snapshot, entries, Translation{}).Sources
	if want := []string{"cnt_env", "opt"}; !slices.Equal(got, want) {
		t.Errorf("roots = %v, want %v", got, want)
	}
}
