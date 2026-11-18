package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/slot/slottest"
)

func TestSlotValuesKeepsOnlyChosenSlots(t *testing.T) {
	got := slotValues(map[string]string{"genome": " grch38/genome/gencode ", "index": "", "annotation": "g/47"})
	if want := []string{"annotation=g/47", "genome=grch38/genome/gencode"}; !slices.Equal(got, want) {
		t.Errorf("slotValues = %v, want %v", got, want)
	}
	if slotValues(nil) != nil {
		t.Errorf("no slots chosen = %v, want nil", slotValues(nil))
	}
}

// The page offers the choices of a strict slot, and for an open one the installed overlays that export what it needs.
func TestHandleHelperSlotsListsTheOverlaysThatFit(t *testing.T) {
	images := t.TempDir()
	slottest.Image(t, images, "grch38/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/g.fa"}})
	slottest.Image(t, images, "grcm39/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/m.fa"}})
	slottest.Image(t, images, "grch38/gtf/gencode/47", []meta.EnvVar{{Key: "ANNOTATION_GTF", Value: "{prefix}/a.gtf"}})

	scripts := t.TempDir()
	script := "#!/bin/bash\n#SLOT: genome env=GENOME_FASTA\n#SLOT: annotation default=grch38/gtf/gencode/47 choices=grch38/gtf/gencode/47\n"
	if err := os.WriteFile(filepath.Join(scripts, "viewer"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := config.GlobalDataPaths
	config.GlobalDataPaths.ImagesDirs = []string{images}
	config.GlobalDataPaths.HelperScriptsDirs = []string{scripts}
	t.Cleanup(func() { config.GlobalDataPaths = prev })

	rec := httptest.NewRecorder()
	(&srv{}).handleHelperSlots(rec, httptest.NewRequest(http.MethodGet, "/api/helpers/viewer/slots", nil), "viewer")
	var got []slotValue
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	if len(got) != 2 || got[0].Name != "genome" || got[1].Name != "annotation" {
		t.Fatalf("slots = %+v", got)
	}
	if want := []string{"grcm39/genome/gencode", "grch38/genome/gencode"}; !slices.Equal(sorted(got[0].Options), sorted(want)) {
		t.Errorf("genome options = %v, want the two genomes and not the annotation", got[0].Options)
	}
	if !slices.Equal(got[1].Options, []string{"grch38/gtf/gencode/47"}) || got[1].Default != "grch38/gtf/gencode/47" || !got[1].Strict {
		t.Errorf("annotation = %+v, want exactly its choices", got[1])
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	slices.Sort(out)
	return out
}
