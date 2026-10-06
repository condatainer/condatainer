package config

import (
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// isolateHome points HOME at a fresh directory and restores everything the test touches.
func isolateHome(t *testing.T) (realHome string) {
	t.Helper()
	realHome = t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv(utils.EnvRealHome, "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	prev, prevJob := Global, scheduler.JobHome
	t.Cleanup(func() { Global, scheduler.JobHome = prev, prevJob })
	return realHome
}

func TestApplyHomeOverride(t *testing.T) {
	realHome := isolateHome(t)
	override := filepath.Join(t.TempDir(), "home")
	settingstest.Override(t, "home_override", override)
	GlobalDataPaths = DataPaths{ImagesDirs: []string{"/stale"}}
	t.Cleanup(func() { GlobalDataPaths = DataPaths{} })

	ApplyHomeOverride()
	ApplyHomeOverride() // a job inheriting both variables applies it again

	if got := os.Getenv("HOME"); got != override {
		t.Errorf("HOME = %q, want %q", got, override)
	}
	if got, _ := utils.RealHome(); got != realHome {
		t.Errorf("RealHome = %q, want %q", got, realHome)
	}
	if len(GlobalDataPaths.ImagesDirs) != 0 {
		t.Errorf("data paths were not reset: %v", GlobalDataPaths.ImagesDirs)
	}
	if scheduler.JobHome != override {
		t.Errorf("JobHome = %q, want %q", scheduler.JobHome, override)
	}
	if want := filepath.Join(override, ".cache", "condatainer"); GetUserCacheDir() != want {
		t.Errorf("cache dir = %q, want %q", GetUserCacheDir(), want)
	}
	if want := filepath.Join(realHome, ".config", "condatainer"); GetUserConfigDir() != want {
		t.Errorf("config dir = %q, want %q", GetUserConfigDir(), want)
	}
}

func TestApplyHomeOverrideUnsetVariable(t *testing.T) {
	realHome := isolateHome(t)
	t.Setenv("CNT_TEST_UNSET_SCRATCH", "")
	settingstest.Override(t, "home_override", "$CNT_TEST_UNSET_SCRATCH/home")

	ApplyHomeOverride()

	if got := os.Getenv("HOME"); got != realHome {
		t.Errorf("HOME = %q, want it left as %q", got, realHome)
	}
	if scheduler.JobHome != "" {
		t.Errorf("JobHome = %q, want empty", scheduler.JobHome)
	}
}
