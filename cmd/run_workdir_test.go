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
