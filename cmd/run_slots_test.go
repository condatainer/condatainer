package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/slot"
	"github.com/condatainer/condatainer/internal/slot/slottest"
)

// installSlotImages makes the overlays the installed set and returns their folder.
func installSlotImages(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prevSources, prevPaths := config.Global.Sources, config.GlobalDataPaths
	config.Global.Sources = []catalog.Spec(nil)
	settingstest.Override(t, "default_distro", "ubuntu24")
	config.GlobalDataPaths.ImagesDirs = []string{dir}
	config.ResetCatalog()
	t.Cleanup(func() {
		config.Global.Sources, config.GlobalDataPaths = prevSources, prevPaths
		config.ResetCatalog()
	})
	t.Chdir(t.TempDir())
	return dir
}

func slotScript(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\n"+text+"echo hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlanSlotsMountsAndRepeatsExactNames(t *testing.T) {
	dir := installSlotImages(t)
	human := slottest.Image(t, dir, "grch38/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/h.fa"}})
	mouse := slottest.Image(t, dir, "grcm39/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/m.fa"}})
	script := slotScript(t, "#SLOT: genome default=grch38/genome/gencode env=GENOME_FASTA\n")

	plan, err := planSlots(context.Background(), script, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Paths(), []string{human}) || !slices.Equal(plan.Flags(), []string{"--slot=" + shellQuote("genome=grch38/genome/gencode")}) {
		t.Errorf("paths = %v, flags = %v, want the default and its exact name", plan.Paths(), plan.Flags())
	}

	plan, err = planSlots(context.Background(), script, []string{"genome=grcm39/genome/gencode"}, false)
	if err != nil || !slices.Equal(plan.Paths(), []string{mouse}) || plan.Mounts[0].Binding.Source != slot.FromFlag {
		t.Errorf("plan = %+v, err = %v, want the flag to win over the default", plan, err)
	}
}

func TestPlanSlotsRefuses(t *testing.T) {
	dir := installSlotImages(t)
	slottest.Image(t, dir, "grch38/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/h.fa"}})
	script := slotScript(t, "#SLOT: genome default=grch38/genome/gencode env=GENOME_FASTA,GENOME_FAI\n")

	if _, err := planSlots(context.Background(), script, nil, false); err == nil || !strings.Contains(err.Error(), "does not export GENOME_FAI") {
		t.Errorf("a missing variable: err = %v", err)
	}

	script = slotScript(t, "#SLOT: genome default=grch38/genome/gencode\n")
	_, err := planSlots(context.Background(), script, []string{"genome=nothing/here"}, false)
	var missing *errSlotMissing
	if !errors.As(err, &missing) || !slices.Equal(missing.Names, []string{"nothing/here"}) {
		t.Errorf("an overlay that is not installed: err = %v", err)
	}

	script = slotScript(t, "#SLOT: index\n")
	_, err = planSlots(context.Background(), script, nil, false)
	var unbound *slot.UnboundError
	if !errors.As(err, &unbound) {
		t.Errorf("a slot with nothing to bind: err = %v", err)
	}
}

func TestPlanSlotsWithNoSlotsIsNil(t *testing.T) {
	installSlotImages(t)
	plan, err := planSlots(context.Background(), slotScript(t, "#DEP: samtools\n"), nil, false)
	if err != nil || plan != nil || plan.Paths() != nil || plan.Flags() != nil {
		t.Errorf("plan = %v, err = %v, want nothing", plan, err)
	}
	if _, err := planSlots(context.Background(), slotScript(t, ""), []string{"genome=a/b"}, false); err == nil ||
		!strings.Contains(err.Error(), "declares no slots") {
		t.Errorf("a --slot for a script with no slots: err = %v", err)
	}
}

func TestSlotSubjectsFeedConditionalHeaders(t *testing.T) {
	dir := installSlotImages(t)
	slottest.Image(t, dir, "grch38/genome/gencode/49", nil)
	slottest.Image(t, dir, "grcm39/genome/gencode/37", nil)
	script := slotScript(t, "#SLOT: genome default=grch38/genome/gencode/49\n#MEM: 8G\n#MEM: 32G genome=grcm39\n#TIME: 2h\n")

	for _, tc := range []struct {
		values []string
		mem    int64
		why    string
	}{
		{nil, 8 << 10, "no condition matched"},
		{[]string{"genome=grcm39/genome/gencode/37"}, 32 << 10, "genome matched grcm39"},
	} {
		plan, err := planSlots(context.Background(), script, tc.values, false)
		if err != nil {
			t.Fatal(err)
		}
		specs, err := scheduler.ReadScriptSpecsFor(script, slotSubjects(script, plan))
		if err != nil {
			t.Fatal(err)
		}
		if specs.Spec.MemPerNodeMB != tc.mem || len(specs.Conditions) != 1 || specs.Conditions[0].Because != tc.why {
			t.Errorf("%v: mem = %d, conditions = %+v, want %d and %q", tc.values, specs.Spec.MemPerNodeMB, specs.Conditions, tc.mem, tc.why)
		}
	}
}

// A script whose slots cannot bind still knows which slots it declares, so a condition on one is read and not refused as unknown.
func TestSlotSubjectsKnowDeclaredSlotsWhenUnbound(t *testing.T) {
	installSlotImages(t)
	script := slotScript(t, "#SLOT: genome\n#MEM: 8G\n#MEM: 32G genome=grcm39\n")
	plan, err := planSlots(context.Background(), script, nil, false)
	if err == nil || plan != nil {
		t.Fatalf("plan = %v, err = %v, want an unbound slot", plan, err)
	}
	specs, err := scheduler.ReadScriptSpecsFor(script, slotSubjects(script, plan))
	if err != nil || specs.Spec.MemPerNodeMB != 8<<10 {
		t.Fatalf("specs = %+v, err = %v", specs, err)
	}
}

// Strict limits the choices, not the completion: a partial tool name resolves first, and what it resolves to must be a choice.
func TestPlanSlotsCompletesAToolThenChecksTheChoices(t *testing.T) {
	dir := installSlotImages(t)
	old := slottest.App(t, dir, "samtools/1.24", nil)
	slottest.App(t, dir, "samtools/1.25", nil)
	script := slotScript(t, "#SLOT: tool default=samtools/1.24 choices=samtools/1.24\n")

	plan, err := planSlots(context.Background(), script, []string{"tool=samtools/1.24"}, false)
	if err != nil || !slices.Equal(plan.Paths(), []string{old}) {
		t.Fatalf("a choice as written: plan = %v, err = %v", plan, err)
	}
	// samtools/1 completes to the newest installed, 1.25, which is not a choice.
	_, err = planSlots(context.Background(), script, []string{"tool=samtools/1"}, false)
	if err == nil || !strings.Contains(err.Error(), "cannot be bound to samtools/1 (samtools/1.25)") {
		t.Errorf("a partial name that completes outside the choices: err = %v", err)
	}
	// Once only 1.24 is a choice and 1.25 is gone, the partial name reaches it.
	if err := os.Remove(filepath.Join(dir, "samtools--1.25.sqf")); err != nil {
		t.Fatal(err)
	}
	config.ResetCatalog()
	plan, err = planSlots(context.Background(), script, []string{"tool=samtools/1"}, false)
	if err != nil || !slices.Equal(plan.Paths(), []string{old}) || plan.Mounts[0].Name != "samtools/1.24" {
		t.Errorf("a partial name that completes to a choice: plan = %v, err = %v", plan, err)
	}
}

// A data overlay is named in full: a shorter name finds nothing, so a slot cannot be bound to it that way.
func TestPlanSlotsDoesNotCompleteADataName(t *testing.T) {
	dir := installSlotImages(t)
	slottest.Image(t, dir, "grch38/genome/gencode", nil)
	script := slotScript(t, "#SLOT: genome\n")
	_, err := planSlots(context.Background(), script, []string{"genome=grch38/genome"}, false)
	var missing *errSlotMissing
	if !errors.As(err, &missing) {
		t.Fatalf("a partial data name: err = %v, want it reported as not installed", err)
	}
	if _, err := planSlots(context.Background(), script, []string{"genome=grch38/genome/gencode"}, false); err != nil {
		t.Errorf("the full data name: %v", err)
	}
}
