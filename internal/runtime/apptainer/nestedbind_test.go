package apptainer

import (
	"slices"
	"testing"
)

// A nested launch drops the inherited bind entries it passes itself, keeps the rest, and leaves a host launch alone.
func TestNestedBindEnv(t *testing.T) {
	prev := insideContainer
	t.Cleanup(func() { insideContainer = prev })

	own := []string{"/tmp", "/lab", "/exe:/.condatainer/bin/condatainer:ro"}
	t.Setenv("APPTAINER_BIND", "/tmp,/exe:/.condatainer/bin/condatainer,/site/data")
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

// A path to drop removes its inherited bind and puts nothing in its place.
func TestNestedBindEnvDropsAPathWithNoReplacement(t *testing.T) {
	prev := insideContainer
	t.Cleanup(func() { insideContainer = prev })
	insideContainer = func() bool { return true }

	t.Setenv("APPTAINER_BIND", "/tmp,/host/relay:/.condatainer/host,/site/data")
	t.Setenv("SINGULARITY_BIND", "/host/relay:/.condatainer/host")
	t.Setenv("APPTAINER_BINDPATH", "")
	set, unset := nestedBindEnv([]string{"/lab", "/.condatainer/host"})
	if want := []string{"APPTAINER_BIND=/tmp,/site/data"}; !slices.Equal(set, want) {
		t.Errorf("set = %v, want %v", set, want)
	}
	if want := []string{"SINGULARITY_BIND"}; !slices.Equal(unset, want) {
		t.Errorf("unset = %v, want %v", unset, want)
	}
}

// A launch that binds all it needs drops every inherited bind variable, inside a container or not.
func TestBindEnvWithNoInheritedBinds(t *testing.T) {
	prev := insideContainer
	t.Cleanup(func() { insideContainer = prev })
	t.Setenv("APPTAINER_BIND", "/tmp,/exe:/.condatainer/bin/condatainer")
	for _, inside := range []bool{true, false} {
		insideContainer = func() bool { return inside }
		set, unset := bindEnv(&ExecOptions{Bind: []string{"/usr"}, NoInheritedBinds: true})
		if set != nil || !slices.Equal(unset, inheritedBindVars) {
			t.Errorf("inside=%v: set=%v unset=%v, want every bind variable unset", inside, set, unset)
		}
	}
}
