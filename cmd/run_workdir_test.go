package cmd

import (
	"os"
	"testing"

	"github.com/condatainer/condatainer/internal/scheduler"
)

func TestSetDefaultWorkDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, declared, want string }{
		{"submission directory when none declared", "", cwd},
		{"declared directory kept", "/data/run", "/data/run"},
	} {
		specs := &scheduler.ScriptSpecs{}
		specs.Control.WorkDir = tc.declared
		setDefaultWorkDir(specs)
		if specs.Control.WorkDir != tc.want {
			t.Errorf("%s: WorkDir = %q, want %q", tc.name, specs.Control.WorkDir, tc.want)
		}
	}
}
