package exec

import (
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/runtime/hostrun"
)

// inheritedHostRun is what a launch inside a container removes of the relay it would inherit.
//   - The outer apptainer exports its binds to the environment, and a nested apptainer would take the relay bind from there.
//   - So the inherited bind and the socket variable are removed, and a container inside a container has no host-run.
func inheritedHostRun() (dropBinds, unsetEnv []string) {
	if !config.IsInsideContainer() {
		return nil, nil
	}
	return []string{hostrun.ContainerDir}, []string{hostrun.EnvSock}
}
