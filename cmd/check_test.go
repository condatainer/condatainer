package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/report"
)

// A request is installed when its name resolved to a file, or the overlay file
// it names is there; an installed one carries its location.
func TestDependencyRecord(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "samtools--1.24.sqf")
	if err := os.WriteFile(installed, []byte("sqf"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := dependencyRecord("samtools/1.24", installed)
	if !got.Installed || got.Path != installed || got.Format != report.FormatSquashFS || got.Size != 3 {
		t.Errorf("installed name = %+v", got)
	}
	if got := dependencyRecord("nosuch/1", "nosuch/1"); got.Installed || got.Path != "" {
		t.Errorf("missing name = %+v", got)
	}
	if got := dependencyRecord(installed, installed); !got.Installed || got.Path != installed {
		t.Errorf("overlay file = %+v", got)
	}
	if got := dependencyRecord(filepath.Join(dir, "gone.sqf"), ""); got.Installed {
		t.Errorf("absent overlay file = %+v", got)
	}
}

// -a makes a missing overlay from the source file beside it, and cannot when
// there is none or the script declares dependencies of its own.
func TestExternalStep(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("conda.yml", "dependencies: []\n")
	write("scripted.sh", "#!/bin/bash\n#DEP:samtools/1.24\n")

	if s := externalStep(filepath.Join(dir, "conda.sqf")); s.Action != report.ActionBuild || s.Runs != report.RunsLocal {
		t.Errorf("with a .yml = %+v", s)
	}
	if s := externalStep(filepath.Join(dir, "none.sqf")); s.Action != report.ActionUnavailable || s.Reason == "" {
		t.Errorf("with no source = %+v", s)
	}
	if s := externalStep(filepath.Join(dir, "scripted.sqf")); s.Action != report.ActionUnavailable || !strings.Contains(s.Reason, "#DEP") {
		t.Errorf("with a .sh that has #DEP = %+v", s)
	}
}

// --dry-run previews -a, and JSON for -a is only the preview.
func TestCheckFlagRules(t *testing.T) {
	defer func(a, d, j bool) { checkAutoInstall, checkInstallDryRun, checkJSON = a, d, j }(checkAutoInstall, checkInstallDryRun, checkJSON)

	checkAutoInstall, checkInstallDryRun, checkJSON = false, true, false
	if err := runCheck(scriptCheckCmd, []string{"x.sh"}); err == nil || !strings.Contains(err.Error(), "-a") {
		t.Errorf("--dry-run without -a: %v", err)
	}
	checkAutoInstall, checkInstallDryRun, checkJSON = true, false, true
	if err := runCheck(scriptCheckCmd, []string{"x.sh"}); err == nil || !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("-a --json without --dry-run: %v", err)
	}
}
