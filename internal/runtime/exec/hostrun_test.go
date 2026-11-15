package exec

import (
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/runtime/hostrun"
)

// A launch inside a container removes the relay it would inherit, so a container inside a container has no host-run.
func TestALaunchInsideAContainerRemovesTheInheritedHostRun(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "1")
	drop, unset := inheritedHostRun()
	if !slices.Equal(drop, []string{hostrun.ContainerDir}) || !slices.Equal(unset, []string{hostrun.EnvSock}) {
		t.Errorf("drop %v, unset %v; want the relay bind dropped and the socket variable unset", drop, unset)
	}
}
