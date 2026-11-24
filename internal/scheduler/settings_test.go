package scheduler

import (
	"os"
	"path/filepath"
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
	settingstest.Override(t, "scheduler.ncpu", "4")
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
		"/usr/bin/sbatch": SchedulerSLURM, "bsub": SchedulerLSF, "/x/qsub": SchedulerPBS,
		"/usr/bin/condor_submit": SchedulerHTCondor, "/x/other": SchedulerUnknown,
	} {
		if got := TypeFromBin(bin); got != want {
			t.Errorf("TypeFromBin(%q) = %q, want %q", bin, got, want)
		}
	}
}

func TestDetectBinFindsCondorSubmit(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "condor_submit")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := DetectBin(); got != bin {
		t.Errorf("DetectBin() = %q, want %q", got, bin)
	}
}

func TestProxyPerJobNamesTheJobKinds(t *testing.T) {
	for _, kind := range []string{ProxyCreate, ProxyHelper, ProxyRun} {
		if ProxyPerJob(kind) {
			t.Errorf("%s gets a proxy by default", kind)
		}
	}
	settingstest.OverrideList(t, "scheduler.proxy_per_job", ProxyCreate, ProxyRun)
	if !ProxyPerJob(ProxyCreate) || !ProxyPerJob(ProxyRun) || ProxyPerJob(ProxyHelper) {
		t.Error("only the named kinds get a proxy")
	}
	settingstest.OverrideList(t, "scheduler.proxy_per_job", ProxyAll)
	if !ProxyPerJob(ProxyCreate) || !ProxyPerJob(ProxyHelper) || !ProxyPerJob(ProxyRun) {
		t.Error("all names every kind")
	}
}
