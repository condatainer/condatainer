package cmd

import (
	"os"
	"testing"

	"github.com/condatainer/condatainer/internal/scheduler"
)

func TestUseSubmitDir(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	specs := &scheduler.ScriptSpecs{}
	if err := useSubmitDir("run.sh", specs); err != nil {
		t.Fatal(err)
	}
	if specs.Control.WorkDir != cwd {
		t.Errorf("WorkDir = %q, want the submission directory %q", specs.Control.WorkDir, cwd)
	}

	specs = &scheduler.ScriptSpecs{}
	specs.Control.WorkDir = "/data/run"
	if err := useSubmitDir("run.sh", specs); err == nil {
		t.Error("a script that sets its own working directory was accepted")
	}
}

func TestOverrideCPUs(t *testing.T) {
	for _, tc := range []struct{ flag, script, want int }{
		{2, 4, 2},
		{0, 4, 4},
		{0, 0, 1},
	} {
		if got := overrideCPUs(tc.flag, tc.script); got != tc.want {
			t.Errorf("overrideCPUs(%d, %d) = %d, want %d", tc.flag, tc.script, got, tc.want)
		}
	}
}
