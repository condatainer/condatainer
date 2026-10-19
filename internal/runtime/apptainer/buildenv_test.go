package apptainer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A build runs without any inherited bind variable; other operations keep them.
func TestBuildDropsInheritedBinds(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-apptainer")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range inheritedBindVars {
		t.Setenv(name, "/x")
	}

	run := func(op string) string {
		var out bytes.Buffer
		if err := runApptainerWithOutput(context.Background(), Bin{Path: script}, op, "", false, nil, &out, nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	build := run("build")
	exec := run("exec")
	for _, name := range inheritedBindVars {
		if strings.Contains(build, name+"=") {
			t.Errorf("build kept %s", name)
		}
		if !strings.Contains(exec, name+"=") {
			t.Errorf("exec dropped %s", name)
		}
	}
}
