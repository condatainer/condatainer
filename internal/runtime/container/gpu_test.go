package container

import (
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"testing"
)

// autoload_gpu: false is the escape hatch for a node whose driver is installed
// but unusable — detection fires on the device node and the container then
// fails to start, with nothing else able to stop it.
func TestDetectGPUFlagsRespectsAutoload(t *testing.T) {
	settingstest.Override(t, "autoload_gpu", "false")
	if flags := DetectGPUFlags(false); len(flags) != 0 {
		t.Errorf("DetectGPUFlags(false) = %v with autoload off, want none", flags)
	}

	// With it on, the result is whatever this host has — the only invariant is
	// that it stops suppressing.
	settingstest.Override(t, "autoload_gpu", "true")
	onFlags := DetectGPUFlags(false)
	if hasNvidiaGPU() && !contains(onFlags, "--nv") {
		t.Errorf("DetectGPUFlags(false) = %v, want --nv on a host with /dev/nvidiactl", onFlags)
	}
	if !hasNvidiaGPU() && !hasRocmGPU() && len(onFlags) != 0 {
		t.Errorf("DetectGPUFlags(false) = %v on a host with no GPU device nodes", onFlags)
	}
}

// A script that declares a GPU requirement must get GPU flags even with
// autoload_gpu:false, as long as the host actually has the device node —
// requested only overrides the config toggle, not host detection.
func TestDetectGPUFlagsRequestedOverridesAutoload(t *testing.T) {
	settingstest.Override(t, "autoload_gpu", "false")

	flags := DetectGPUFlags(true)
	if hasNvidiaGPU() && !contains(flags, "--nv") {
		t.Errorf("DetectGPUFlags(true) = %v, want --nv on a host with /dev/nvidiactl even with autoload off", flags)
	}
	if hasRocmGPU() && !contains(flags, "--rocm") {
		t.Errorf("DetectGPUFlags(true) = %v, want --rocm on a host with /dev/kfd even with autoload off", flags)
	}
	if !hasNvidiaGPU() && !hasRocmGPU() && len(flags) != 0 {
		t.Errorf("DetectGPUFlags(true) = %v on a host with no GPU device nodes", flags)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// The default has to stay on: turning it off by accident silently drops GPU
// access from every container.
func TestAutoloadGPUDefaultsOn(t *testing.T) {
	if !AutoloadGPU() {
		t.Error("autoload_gpu defaulted to false")
	}
}
