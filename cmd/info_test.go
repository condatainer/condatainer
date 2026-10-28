package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/conda"
)

func TestCondaOfKeepsPackagesAsAList(t *testing.T) {
	if condaOf(nil) != nil {
		t.Error("an image with no history has a conda block")
	}
	got := condaOf(&conda.CondaInfo{Channels: []string{"conda-forge"}})
	if got == nil || got.Packages == nil {
		t.Errorf("conda block = %+v, want packages as an empty list", got)
	}
}

// The tools print local time, so a reading is given the local zone and not UTC.
func TestIsoTimeReadsTheClockInTheLocalZone(t *testing.T) {
	old := time.Local
	t.Cleanup(func() { time.Local = old })
	time.Local = time.FixedZone("EDT", -4*3600)

	if got := isoTime("Wed Sep 23 12:11:25 2026"); got != "2026-09-23T12:11:25-04:00" {
		t.Errorf("isoTime = %q", got)
	}
	if got := isoTime("<not available>"); !strings.Contains(got, "not available") {
		t.Errorf("an unparsed time was changed: %q", got)
	}
}

// Only a payload entry that is not the Conda environment counts, and a .sqf's
// own metadata directory is not one.
func TestEntriesOutsideEnv(t *testing.T) {
	if got := entriesOutsideEnv([]string{".cnt", "cnt_env"}); len(got) != 0 {
		t.Errorf("a plain environment has %v outside it", got)
	}
	got := entriesOutsideEnv([]string{".cnt", "cnt_env", "opt", "usr"})
	if len(got) != 2 || got[0] != "opt" || got[1] != "usr" {
		t.Errorf("outside = %v", got)
	}
}
