package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A command that runs work ends through exit, which prints the usage line first. os.Exit skips deferred calls, so a bare one in these files would end a run with no line.
func TestWorkCommandsExitThroughExit(t *testing.T) {
	for _, file := range []string{
		"run.go", "exec.go", "root.go", "create.go", "create_needed_by.go", "project_restore.go", "shared.go",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "os.Exit(") && !(file == "shared.go" && strings.TrimSpace(line) == "os.Exit(code)") {
				t.Errorf("%s:%d calls os.Exit; use exit", file, i+1)
			}
		}
	}
}

// A build that runs outside a graph marks the run as worked on the line before, so the usage line prints for it.
func TestDirectBuildsMarkWork(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(line, ".Build(ctx") && (i == 0 || !strings.Contains(lines[i-1], "usage.Worked()")) {
				t.Errorf("%s:%d builds without usage.Worked() on the line before", file, i+1)
			}
		}
	}
}
