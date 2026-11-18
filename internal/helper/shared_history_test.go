package helper

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

func TestParamsFromRequired(t *testing.T) {
	deps := []string{"base", "r/{POSIT_R}", "rstudio-server", "r-build-essential"}
	required := []string{"r-build-essential", "r/4.4.3", "rstudio-server"}
	got := paramsFromRequired(deps, required)
	if want := map[string]string{"POSIT_R": "4.4.3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := paramsFromRequired([]string{"rstudio-server"}, []string{"rstudio-server"}); len(got) != 0 {
		t.Fatalf("deps with no tokens must recover nothing, got %v", got)
	}
}

// A run with only slots is reusable, and a slot the helper no longer declares or takes is dropped.
func TestListReusableOffersSlotsTheHelperStillTakes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, lock.DirName), 0o775); err != nil {
		t.Fatal(err)
	}
	slots := map[string]string{"genome": "grch38/genome/gencode/49", "gone": "x/y/1"}
	if err := RecordUsed(root, "igv", ".", nil, nil, slots); err != nil {
		t.Fatal(err)
	}
	decls := slotDecls(t, "#SLOT: genome choices=grch38/genome/gencode/49\n")
	got, err := ListReusable(root, "igv", ".", nil, decls)
	if err != nil || len(got) != 1 || len(got[0].Slots) != 1 || got[0].Slots["genome"] != "grch38/genome/gencode/49" {
		t.Fatalf("ListReusable = %+v, %v", got, err)
	}
	strict := slotDecls(t, "#SLOT: genome choices=grcm39/genome/gencode\n")
	if got, _ := ListReusable(root, "igv", ".", nil, strict); len(got) != 0 {
		t.Errorf("an overlay outside the strict choices was offered: %+v", got)
	}
}
