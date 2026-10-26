package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScriptCommandReadsTheInterpreterFromTheFirstLine(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string][]string{
		"#!/usr/bin/env python3\nprint()\n": {"/usr/bin/env", "python3"},
		"#!/bin/bash -e\necho\n":            {"/bin/bash", "-e"},
		"#!/bin/bash\necho\n":               {"/bin/bash"},
	} {
		script := filepath.Join(dir, "s")
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := scriptCommand(script, []string{"a", "b"})
		want = append(want, script, "a", "b")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v, %v; want %v", body, got, err, want)
		}
	}
}

func TestRequireInterpreterLineNamesTheAllowedForms(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{"echo hi\n", "#!/bin/sh\n", "#!/usr/bin/env -S bash -e\n"} {
		script := filepath.Join(dir, "s")
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		err := requireInterpreterLine(script)
		if err == nil || !strings.Contains(err.Error(), "#!/bin/env <interpreter>") {
			t.Errorf("%q: %v", body, err)
		}
	}
}
