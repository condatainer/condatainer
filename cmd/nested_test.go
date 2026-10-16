package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestNestedRunKeyNormalizesAndRefuses(t *testing.T) {
	k, _ := settings.Lookup("nested_run")
	for in, want := range map[string]string{"auto": "auto", " TRUE ": "true", "False": "false"} {
		if got, err := k.Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := k.Parse("maybe"); err == nil {
		t.Error("nested_run accepted an unknown value")
	}
}

// libexec's apptainer is the only source: bound when installed, and the mode decides what its absence means.
func TestNestedRun(t *testing.T) {
	for _, installed := range []bool{true, false} {
		dir := filepath.Join(t.TempDir(), "libexec")
		bin := filepath.Join(dir, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		names := []string{"micromamba"}
		if installed {
			names = append(names, "apptainer")
		}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("CNT_LIBEXEC", dir)

		for mode, want := range map[string]struct {
			bind bool
			err  string
		}{
			nestedRunAuto:  {bind: installed},
			nestedRunFalse: {},
			nestedRunTrue:  {bind: installed},
		} {
			if mode == nestedRunTrue && !installed {
				want.err = "update --libexec apptainer"
			}
			settingstest.Override(t, "nested_run", mode)
			bind, err := nestedRun()
			if bind != want.bind || (want.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want.err)) {
				t.Errorf("installed=%v nested_run=%s: nestedRun = %v, %v; want %v, error containing %q", installed, mode, bind, err, want.bind, want.err)
			}
		}
	}
}
