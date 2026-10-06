package scheduler

import (
	"os"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestEnabledNeedsSubmitJobAndARunnableBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settingstest.Override(t, "scheduler.bin", exe)
	if !Enabled() {
		t.Error("submit_job defaults on and the binary runs")
	}
	settingstest.Override(t, "scheduler.submit_job", "false")
	if Enabled() {
		t.Error("submit_job off disables submission")
	}
}

func TestEnabledIsOffWhenTheBinaryCannotRun(t *testing.T) {
	settingstest.Override(t, "scheduler.bin", "/nonexistent/sbatch")
	if Enabled() {
		t.Error("a missing binary disables submission")
	}
}

func TestDefaultSpecAndTimeout(t *testing.T) {
	settingstest.Override(t, "scheduler.ncpus", "4")
	settingstest.Override(t, "scheduler.mem", "8g")
	settingstest.Override(t, "scheduler.time", "01:30:00")
	settingstest.Override(t, "scheduler.timeout", "5")
	spec := DefaultSpec()
	if spec.CpusPerTask != 4 || spec.MemPerNodeMB != 8192 || spec.Time != 90*time.Minute {
		t.Errorf("spec = %+v", spec)
	}
	if CommandTimeout() != 5*time.Second {
		t.Errorf("timeout = %v", CommandTimeout())
	}
}

func TestTypeFromBin(t *testing.T) {
	for bin, want := range map[string]SchedulerType{
		"/usr/bin/sbatch": SchedulerSLURM, "bsub": SchedulerLSF, "/x/qsub": SchedulerPBS, "/x/other": SchedulerUnknown,
	} {
		if got := TypeFromBin(bin); got != want {
			t.Errorf("TypeFromBin(%q) = %q, want %q", bin, got, want)
		}
	}
}
