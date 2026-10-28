package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/report"
)

// Each planned step becomes the action it is: an installed overlay is adopted,
// a pull fetches with its copy's keys, and a build says where it runs.
func TestReportStepNamesTheAction(t *testing.T) {
	identity := meta.KeyRef{Scheme: "definition-identity-v1", SHA256: "9f2c"}
	installed := filepath.Join(t.TempDir(), "tool--1.sqf")
	if err := os.WriteFile(installed, []byte("sqf"), 0o644); err != nil {
		t.Fatal(err)
	}

	adopt := reportStep(build.PlannedStep{Name: "tool/1", Installed: true, Path: installed})
	if adopt.Action != report.ActionAdopt || adopt.Size != 3 || adopt.Format != report.FormatSquashFS {
		t.Errorf("adopt = %+v", adopt)
	}

	fetch := reportStep(build.PlannedStep{Name: "os/1", Pull: true, Endpoint: "ghcr.io/lab/cnt",
		Digest: "sha256:abcd", Identity: identity, Path: "/images/os--1.sqf"})
	if fetch.Action != report.ActionFetch || fetch.Endpoint != "ghcr.io/lab/cnt" || fetch.Digest != "sha256:abcd" ||
		fetch.Identity != identity || fetch.Runs != "" || fetch.Size != 0 {
		t.Errorf("fetch = %+v", fetch)
	}

	local := reportStep(build.PlannedStep{Name: "app/1", Path: "/images/app--1.sqf"})
	job := reportStep(build.PlannedStep{Name: "data/1", InJob: true, Path: "/images/data--1.sqf"})
	if local.Action != report.ActionBuild || local.Runs != report.RunsLocal || job.Runs != report.RunsJob {
		t.Errorf("local = %+v, job = %+v", local, job)
	}
}

// A step's dependencies keep their role, and an installed one its location.
func TestReportStepCarriesItsDependencies(t *testing.T) {
	step := reportStep(build.PlannedStep{Name: "grch38/salmon/1.10.2/gencode47-k31", Deps: []build.PlannedDep{
		{Name: "salmon/1.10.2", Type: "app", Role: meta.RoleApp},
		{Name: "grch38/transcript/gencode/47", Type: "data", Role: meta.RoleData, Installed: true, Path: "/images/t.sqf"},
	}})
	if len(step.Deps) != 2 {
		t.Fatalf("deps = %+v", step.Deps)
	}
	if d := step.Deps[0]; d.Name != "salmon/1.10.2" || d.Role != meta.RoleApp || d.Installed || d.Path != "" {
		t.Errorf("app dep = %+v", d)
	}
	if d := step.Deps[1]; d.Role != meta.RoleData || !d.Installed || d.Path != "/images/t.sqf" || d.Format != report.FormatSquashFS {
		t.Errorf("installed data dep = %+v", d)
	}
}
