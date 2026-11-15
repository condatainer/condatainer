package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/registry"
)

func TestParseRemoveTarget(t *testing.T) {
	got, err := parseRemoveTarget("star/2.7.11b@9f2c1ab", "arm64")
	if err != nil || got != (registry.RemoveTarget{Name: "star/2.7.11b", Identity: "9f2c1ab", Arch: "arm64"}) {
		t.Errorf("name@identity = (%+v, %v)", got, err)
	}
	got, err = parseRemoveTarget("star--2.7.11b", "")
	if err != nil || got != (registry.RemoveTarget{Name: "star/2.7.11b"}) {
		t.Errorf("name = (%+v, %v), want every build", got, err)
	}
	if _, err := parseRemoveTarget("star/2.7.11b:eq-9f2c1ab4d3e8", ""); err == nil {
		t.Error("an equivalence key was taken as a build")
	}
}
