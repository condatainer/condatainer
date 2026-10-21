package project

import (
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/project/lock"
)

// pinOf pins artifact for the architecture the tests run on.
func pinOf(artifact string) lock.PinEntry {
	return lock.PinEntry{Artifacts: map[string]string{meta.NativeArch(): artifact}}
}

// manualPinOf is pinOf for a pin the project recorded.
func manualPinOf(artifact string) lock.PinEntry {
	pin := pinOf(artifact)
	pin.Manual = true
	return pin
}
