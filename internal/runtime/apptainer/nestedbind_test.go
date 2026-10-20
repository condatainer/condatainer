package apptainer

import (
	"slices"
	"testing"
)

// A nested launch drops the inherited bind entries it passes itself, keeps the rest, and leaves a host launch alone.
func TestNestedBindEnv(t *testing.T) {
	prev := insideContainer
	t.Cleanup(func() { insideContainer = prev })

	own := []string{"/tmp", "/lab", "/exe:/.cnt_bin/condatainer:ro"}
	t.Setenv("APPTAINER_BIND", "/tmp,/exe:/.cnt_bin/condatainer,/site/data")
	t.Setenv("SINGULARITY_BIND", "/tmp,/lab")
	t.Setenv("APPTAINER_BINDPATH", "")

	insideContainer = func() bool { return true }
	set, unset := nestedBindEnv(own)
	if want := []string{"APPTAINER_BIND=/site/data"}; !slices.Equal(set, want) {
		t.Errorf("set = %v, want %v", set, want)
	}
	if want := []string{"SINGULARITY_BIND"}; !slices.Equal(unset, want) {
		t.Errorf("unset = %v, want %v", unset, want)
	}

	insideContainer = func() bool { return false }
	if set, unset := nestedBindEnv(own); set != nil || unset != nil {
		t.Errorf("host launch changed the environment: set=%v unset=%v", set, unset)
	}
}
