package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

func TestProjectValidateSucceedsOnACompleteProject(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	base := vendorArtifact(t, root, "ubuntu24/base", "echo base\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	l.Pins[lock.BaseKey] = pinOf(base)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "project", "validate", "--lock-only"); err != nil {
		t.Fatalf("validate failed on a complete project: %v", err)
	}
}

// Validate also asks whether the pins are on this machine; --lock-only does not.
// A complete lock passes the first and fails the second when nothing is
// installed.
func TestProjectValidateFailsWhenAPinIsNotInstalled(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	base := vendorArtifact(t, root, "ubuntu24/base", "echo base\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	l.Pins[lock.BaseKey] = pinOf(base)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "project", "validate", "--lock-only"); err != nil {
		t.Fatalf("--lock-only failed: %v", err)
	}
	out, err := run(t, "project", "validate", "--json")
	if err == nil {
		t.Fatalf("validate passed with nothing installed:\n%s", out)
	}
	if !strings.Contains(out, "is not installed here") {
		t.Errorf("validate does not say which pin is missing:\n%s", out)
	}
}

// A declaration the lock cannot reproduce fails validate, even when every other
// declaration is pinned. Nothing in a script silences it: freezing the overlay
// is what closes it, and the finding says so.
func TestProjectValidateFailsOnAnUnpinnableDeclaration(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\n#DEP: env.img\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := runConsole(t, "project", "validate")
	if err == nil {
		t.Fatal("validate passed a project whose environment is a writable overlay")
	}
	if !strings.Contains(out, "overlay freeze") {
		t.Errorf("validate does not point at freeze:\n%s", out)
	}
}

// A #DEP: below the header is inert, and validate says so rather than passing.
func TestProjectValidateFailsOnAnInertDeclaration(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n#DEP: cutadapt/5.0\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "project", "validate"); err == nil {
		t.Fatal("validate passed a project with an inert declaration")
	}
}

func TestProjectValidateJSONReportsWhatIsWrong(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")

	out, err := run(t, "project", "validate", "--json")
	if err == nil {
		t.Fatal("validate passed with nothing selected")
	}
	var report struct {
		SchemaVersion int  `json:"schema_version"`
		Valid         bool `json:"valid"`
		Problems      []any
		Unselected    []struct {
			Request string   `json:"request"`
			Scripts []string `json:"scripts"`
		} `json:"unpinned"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.Valid || report.SchemaVersion != 1 || report.Problems == nil {
		t.Errorf("report = %+v", report)
	}
	if len(report.Unselected) != 1 || report.Unselected[0].Request != "star/2.7.11b" {
		t.Fatalf("unpinned = %#v", report.Unselected)
	}
	if len(report.Unselected[0].Scripts) != 1 || report.Unselected[0].Scripts[0] != "run.sh" {
		t.Errorf("scripts = %v, want the declaring script", report.Unselected[0].Scripts)
	}
}
