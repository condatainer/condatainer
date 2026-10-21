package lock

import "github.com/condatainer/condatainer/internal/artifact/meta"

// pinOf pins artifact for the architecture the tests run on.
func pinOf(artifact string) PinEntry {
	return PinEntry{Artifacts: map[string]string{meta.NativeArch(): artifact}}
}

// manualPinOf is pinOf for a pin the project recorded.
func manualPinOf(artifact string) PinEntry {
	pin := pinOf(artifact)
	pin.Manual = true
	return pin
}
