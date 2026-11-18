package helper

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/slot"
	"github.com/condatainer/condatainer/internal/slot/slottest"
	"github.com/condatainer/condatainer/internal/utils"
)

func slotDecls(t *testing.T, text string) []slot.Decl {
	t.Helper()
	decls, err := slot.ParseHelper(utils.ScanAnnotations([]byte("#!/bin/bash\n" + text)))
	if err != nil {
		t.Fatal(err)
	}
	return decls
}

func TestResolveSlotsMountsDefaultsAndFlagsInDeclaredOrder(t *testing.T) {
	dir := installOverlays(t)
	human := slottest.Image(t, dir, "grch38/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/h.fa"}})
	mouse := slottest.Image(t, dir, "grcm39/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/m.fa"}})
	gtf := slottest.Image(t, dir, "grch38/gtf/gencode/47", []meta.EnvVar{{Key: "ANNOTATION_GTF", Value: "{prefix}/a.gtf"}})
	decls := slotDecls(t, "#SLOT: genome default=grch38/genome/gencode env=GENOME_FASTA\n#SLOT: annotation default=grch38/gtf/gencode/47 env=ANNOTATION_GTF\n")

	got, err := ResolveSlots(context.Background(), t.TempDir(), decls, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if paths := slotPaths(got); !slices.Equal(paths, []string{human, gtf}) {
		t.Errorf("default paths = %v, want the genome then the annotation", paths)
	}
	if got[0].Name != "grch38/genome/gencode" || got[0].Binding.Source != slot.FromDefault {
		t.Errorf("first mount = %+v", got[0])
	}

	got, err = ResolveSlots(context.Background(), t.TempDir(), decls, []string{"genome=grcm39/genome/gencode"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if paths := slotPaths(got); !slices.Equal(paths, []string{mouse, gtf}) || got[0].Binding.Source != slot.FromFlag {
		t.Errorf("flag paths = %v, want the mouse genome by flag, still first", paths)
	}
}

func TestResolveSlotsRefusesWhatTheOverlayLacks(t *testing.T) {
	dir := installOverlays(t)
	slottest.Image(t, dir, "grch38/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/h.fa"}})
	decls := slotDecls(t, "#SLOT: genome default=grch38/genome/gencode env=GENOME_FASTA,GENOME_FAI\n")

	_, err := ResolveSlots(context.Background(), t.TempDir(), decls, nil, true)
	if err == nil || !strings.Contains(err.Error(), "does not export GENOME_FAI") {
		t.Fatalf("err = %v, want the missing variable named", err)
	}
}

func TestResolveSlotsRefusesTwoSlotsExportingOneVariable(t *testing.T) {
	dir := installOverlays(t)
	slottest.Image(t, dir, "a/genome", []meta.EnvVar{{Key: "REF", Value: "{prefix}/a.fa"}})
	slottest.Image(t, dir, "b/genome", []meta.EnvVar{{Key: "REF", Value: "{prefix}/b.fa"}})
	decls := slotDecls(t, "#SLOT: one default=a/genome env=REF\n#SLOT: two default=b/genome env=REF\n")

	_, err := ResolveSlots(context.Background(), t.TempDir(), decls, nil, true)
	if err == nil || !strings.Contains(err.Error(), "both export REF with different values") {
		t.Fatalf("err = %v, want the collision named", err)
	}
}

func TestResolveSlotsReportsAnUnboundSlot(t *testing.T) {
	installOverlays(t)
	decls := slotDecls(t, "#SLOT: genome\n#SLOT: extra optional=true\n")

	_, err := ResolveSlots(context.Background(), t.TempDir(), decls, nil, true)
	var unbound *slot.UnboundError
	if !errors.As(err, &unbound) || len(unbound.Slots) != 1 || unbound.Slots[0].Name != "genome" {
		t.Fatalf("err = %v, want genome reported unbound", err)
	}
}

func TestResolveSlotsLeavesAnOptionalSlotUnbound(t *testing.T) {
	installOverlays(t)
	decls := slotDecls(t, "#SLOT: extra optional=true\n")

	got, err := ResolveSlots(context.Background(), t.TempDir(), decls, nil, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("mounts = %v, err = %v, want nothing mounted", got, err)
	}
}

func TestHelperMetaReadsSlots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc")
	text := "#!/bin/bash\n#DEP: igv\n#SLOT: genome optional=true env=GENOME_FASTA\n"
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	meta, err := ParseHelperScriptMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Slots) != 1 || meta.Slots[0].Name != "genome" || !meta.Slots[0].Optional {
		t.Errorf("Slots = %+v", meta.Slots)
	}

	if err := os.WriteFile(path, []byte("#!/bin/bash\n#SLOT: genome strict=false\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseHelperScriptMeta(path); err == nil || !strings.Contains(err.Error(), "strict= needs choices=") {
		t.Errorf("a bad slot: err = %v, want it named", err)
	}
}

func TestPrintSlotsListsDefaultsChoicesAndNeeds(t *testing.T) {
	decls := slotDecls(t, "#SLOT: genome default=a/g choices=a/g,b/g env=GENOME_FASTA\n"+
		"#SLOT: index\n#SLOT: extra optional=true bin=tool\n#SLOT: ann default=a/x choices=a/x strict=false\n")

	var out strings.Builder
	printSlots(&out, decls)
	got := out.String()
	for _, want := range []string{
		"Slots (--slot NAME=OVERLAY):",
		"genome       default: a/g",
		"choices: a/g, b/g",
		"needs: $GENOME_FASTA",
		"index        required",
		"extra        optional",
		"needs: tool on PATH",
		"suggested: a/x",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("slots help lacks %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 80 {
			t.Errorf("line over 80 columns: %q", line)
		}
	}
	var none strings.Builder
	printSlots(&none, nil)
	if none.Len() != 0 {
		t.Errorf("no slots printed %q", none.String())
	}
}

func TestRestoreSlotsKeepsWhatTheScriptStillTakes(t *testing.T) {
	decls := slotDecls(t, "#SLOT: genome choices=a/genome/49\n#SLOT: index\n#SLOT: ann choices=a/ann strict=false\n")
	recorded := map[string]string{
		"genome": "a/genome/49", // one of the choices
		"index":  "a/star/2.7",
		"ann":    "z/ann/1", // outside the choices, but the slot is not strict
		"gone":   "x/y/1",   // no longer declared
	}
	got := RestoreSlots(decls, recorded)
	want := []string{"genome=a/genome/49", "index=a/star/2.7", "ann=z/ann/1"}
	if !slices.Equal(got, want) {
		t.Errorf("RestoreSlots = %v, want %v", got, want)
	}

	recorded["genome"] = "b/genome/1"
	if got := RestoreSlots(decls, recorded); slices.Contains(got, "genome=b/genome/1") {
		t.Errorf("a strict slot kept an overlay outside its choices: %v", got)
	}
	if got := RestoreSlots(decls, nil); got != nil {
		t.Errorf("no record gave %v", got)
	}
}

func TestHelperRunRecordsSlotsInHistory(t *testing.T) {
	plan := &RunPlan{Slots: []SlotMount{
		{Binding: slot.Binding{Decl: slot.Decl{Name: "genome"}}, Name: "grch38/genome/gencode/49"},
		{Binding: slot.Binding{Decl: slot.Decl{Name: "index"}}, Name: "grch38/star/2.7.11b"},
	}}
	run := HelperRun{ID: "x", Slots: plan.SlotNames()}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var back HelperRun
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Slots["genome"] != "grch38/genome/gencode/49" || back.Slots["index"] != "grch38/star/2.7.11b" {
		t.Errorf("slots = %v", back.Slots)
	}
	if (&RunPlan{}).SlotNames() != nil {
		t.Error("a plan with no slots should record none")
	}
	if data, _ := json.Marshal(HelperRun{ID: "y"}); strings.Contains(string(data), "slots") {
		t.Errorf("a run with no slots wrote %s", data)
	}
}

// Strict limits the choices, not the completion. An unlisted value is resolved without building, then checked against the choices.
func TestResolveSlotsChecksWhatAnUnlistedValueResolvesTo(t *testing.T) {
	dir := installOverlays(t)
	old := slottest.App(t, dir, "samtools/1.24", nil)
	decls := slotDecls(t, "#SLOT: tool default=samtools/1.24 choices=samtools/1.24\n")

	got, err := ResolveSlots(context.Background(), t.TempDir(), decls, []string{"tool=samtools/1"}, true)
	if err != nil || !slices.Equal(slotPaths(got), []string{old}) || got[0].Name != "samtools/1.24" {
		t.Fatalf("a partial name that completes to a choice: %+v, err = %v", got, err)
	}

	slottest.App(t, dir, "samtools/1.25", nil)
	if _, err = ResolveSlots(context.Background(), t.TempDir(), decls, []string{"tool=samtools/1"}, true); err == nil ||
		!strings.Contains(err.Error(), "cannot be bound to samtools/1 (samtools/1.25)") {
		t.Errorf("a partial name that completes outside the choices: err = %v", err)
	}
	// A value nothing installed answers is refused, not built.
	if _, err = ResolveSlots(context.Background(), t.TempDir(), decls, []string{"tool=nothing/1.0"}, true); err == nil ||
		!strings.Contains(err.Error(), "cannot be bound to nothing/1.0") {
		t.Errorf("an unlisted overlay nothing installed answers: err = %v", err)
	}
}

// A data overlay is named in full, so a shorter name does not reach it.
func TestResolveSlotsDoesNotCompleteADataName(t *testing.T) {
	dir := installOverlays(t)
	slottest.Image(t, dir, "grch38/genome/gencode", nil)
	decls := slotDecls(t, "#SLOT: genome choices=grch38/genome/gencode\n")
	if _, err := ResolveSlots(context.Background(), t.TempDir(), decls, []string{"genome=grch38/genome"}, true); err == nil ||
		!strings.Contains(err.Error(), "cannot be bound to grch38/genome") {
		t.Errorf("a partial data name: err = %v", err)
	}
}

// Standing in a project, a slot overlay that no pin answers mounts from what is installed, and the launch says so.
func TestResolveSlotsNotesAnUnpinnedOverlayInAProject(t *testing.T) {
	dir := installOverlays(t)
	slottest.Image(t, dir, "grch38/genome/gencode", nil)
	root := t.TempDir()
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	decls := slotDecls(t, "#SLOT: genome default=grch38/genome/gencode\n")

	var log strings.Builder
	ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&log, nil)))
	if _, err := ResolveSlots(ctx, root, decls, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "slot genome: grch38/genome/gencode is not pinned in the project") {
		t.Errorf("log = %q, want the unpinned note", log.String())
	}

	// Outside a project there is nothing to be pinned in.
	log.Reset()
	if _, err := ResolveSlots(ctx, t.TempDir(), decls, nil, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "not pinned") {
		t.Errorf("log = %q, want no note outside a project", log.String())
	}
}
