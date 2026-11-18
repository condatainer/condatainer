package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/helper"
)

func TestApplyHistoryRunRestoresSlotsTheScriptStillDeclares(t *testing.T) {
	script := filepath.Join(t.TempDir(), "igv")
	text := "#!/bin/bash\n#SLOT: genome default=a/genome/1 choices=a/genome/1,a/genome/49\n#SLOT: ann optional=true\n"
	if err := os.WriteFile(script, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := helper.RunOptions{ScriptPath: script}
	ApplyHistoryRun(&opts, &helper.HelperRun{Slots: map[string]string{
		"genome": "a/genome/49", "ann": "a/ann/1", "gone": "x/y/1",
	}})
	if want := []string{"genome=a/genome/49", "ann=a/ann/1"}; !slices.Equal(opts.Slots, want) {
		t.Errorf("Slots = %v, want %v", opts.Slots, want)
	}
}

func TestFormatRunSlotsListsByName(t *testing.T) {
	r := &helper.HelperRun{Slots: map[string]string{"index": "b/star/2", "genome": "a/genome/1"}}
	if got := formatRunSlots(r); got != "genome=a/genome/1 index=b/star/2" {
		t.Errorf("formatRunSlots = %q", got)
	}
	if formatRunSlots(&helper.HelperRun{}) != "" {
		t.Error("a run with no slots should list none")
	}
}
