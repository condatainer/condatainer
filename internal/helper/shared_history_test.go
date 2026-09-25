package helper

import (
	"reflect"
	"testing"
)

func TestParamsFromRequired(t *testing.T) {
	template := "base r/{POSIT_R} rstudio-server r-build-essential"
	required := []string{"r-build-essential", "r/4.4.3", "rstudio-server"}
	got := paramsFromRequired(template, required)
	if want := map[string]string{"POSIT_R": "4.4.3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := paramsFromRequired("rstudio-server", []string{"rstudio-server"}); len(got) != 0 {
		t.Fatalf("a template with no tokens must recover nothing, got %v", got)
	}
}
