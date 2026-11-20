package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/runtime/container"
)

func TestCallerExecutable(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		self string
		want string
	}{
		{"a condatainer command", []string{"condatainer", "scheduler", "jobs"}, "/home/u/bin/condatainer_go", "/home/u/bin/condatainer_go"},
		{"the copy bound into the container", []string{"condatainer", "run"}, container.BoundExecPath, ""},
		{"a path that is not absolute", []string{"condatainer", "run"}, "condatainer", ""},
		{"a command that is not condatainer", []string{"sbatch", "x"}, "/home/u/bin/condatainer_go", ""},
		{"no command", nil, "/home/u/bin/condatainer_go", ""},
	}
	for _, tt := range tests {
		if got := callerExecutable(tt.argv, tt.self); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
