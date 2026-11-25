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

func TestApplyHistoryRunRestoresAccountAndPartition(t *testing.T) {
	opts := helper.RunOptions{Account: "typed", Partition: "typed"}
	ApplyHistoryRun(&opts, &helper.HelperRun{Account: "lab", Partition: "gpu"})
	if opts.Account != "lab" || opts.Partition != "gpu" {
		t.Errorf("Account/Partition = %q/%q, want lab/gpu", opts.Account, opts.Partition)
	}
	ApplyHistoryRun(&opts, &helper.HelperRun{})
	if opts.Account != "lab" || opts.Partition != "gpu" {
		t.Errorf("a record with none changed them to %q/%q", opts.Account, opts.Partition)
	}
}

func TestApplyHistoryRunRestoresNoProject(t *testing.T) {
	var opts helper.RunOptions
	ApplyHistoryRun(&opts, &helper.HelperRun{})
	if opts.NoProject {
		t.Error("a record without it set NoProject")
	}
	ApplyHistoryRun(&opts, &helper.HelperRun{NoProject: true})
	if !opts.NoProject {
		t.Error("a record with it did not set NoProject")
	}
}
