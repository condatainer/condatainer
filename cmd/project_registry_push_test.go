package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/publish"
	"github.com/condatainer/condatainer/internal/report"
)

// Push holds a project to what validate asks, so a lock that cannot reproduce
// the project is never uploaded. The refusal is reached before any registry is
// contacted.
func TestProjectPushRefusesWhatValidateRefuses(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\n#DEP: env.img\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")

	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	_, err := runConsole(t, "project", "registry", "push")
	if err == nil {
		t.Fatal("push published a project validate rejects")
	}
	if !strings.Contains(err.Error(), "env.img") {
		t.Errorf("the refusal does not name the declaration: %v", err)
	}
}

// Each disposition lands on its own record, and a run adds the outcome.
func TestPushDocReportsEachStep(t *testing.T) {
	remote := &lock.Remote{Repository: "ghcr.io/lab/cnt", ManifestDigest: "sha256:aa"}
	plan := &publish.Plan{Repository: "ghcr.io/lab/cnt", Audience: "public", Steps: []publish.Step{
		{Artifact: "a", Name: "a", Disposition: publish.Upload, Tags: []string{"a__1"}, Channels: []string{"conda-forge"}},
		{Artifact: "b", Name: "b", Disposition: publish.Present, Remote: remote},
		{Artifact: "c", Name: "c", Disposition: publish.Served, Remote: &lock.Remote{Repository: "ghcr.io/src/cnt", ManifestDigest: "sha256:cc"}},
		{Artifact: "d", Name: "d", Disposition: publish.Upload, Tags: []string{"d__1"}},
		{Artifact: "e", Name: "e", Disposition: publish.Refused, Reason: "not allowed"},
	}}

	preview := pushDoc("/p", plan, &lock.Verified{}, nil)
	if preview.Repository != "ghcr.io/lab/cnt" || len(preview.Pushes) != 5 {
		t.Fatalf("preview = %+v", preview)
	}
	for i, want := range []report.Outcome{"", "", "", "", report.OutcomeRefused} {
		if got := preview.Pushes[i].Outcome; got != want {
			t.Errorf("preview %d outcome = %q, want %q", i, got, want)
		}
	}
	if !preview.Pushes[1].Existing || preview.Pushes[1].Digest != "sha256:aa" || len(preview.Pushes[0].Channels) != 1 {
		t.Errorf("present or channels wrong: %+v %+v", preview.Pushes[1], preview.Pushes[0])
	}
	if got := preview.Pushes[2]; got.Served == nil || got.Served.Repository != "ghcr.io/src/cnt" || got.Digest != "" {
		t.Errorf("served = %+v", got)
	}

	uploaded := plan.Steps[0]
	uploaded.Path, uploaded.Remote = "/img/a.sqf", &lock.Remote{Repository: "ghcr.io/lab/cnt", ManifestDigest: "sha256:new"}
	run := &publish.Report{
		Published: []publish.Step{uploaded, plan.Steps[1], plan.Steps[2]},
		Failures:  []publish.Failure{{Step: plan.Steps[3], Reason: "network down"}},
	}
	done := pushDoc("/p", plan, &lock.Verified{}, run)
	for i, want := range []report.Outcome{report.OutcomeDone, report.OutcomeDone, report.OutcomeSkipped, report.OutcomeFailed} {
		if got := done.Pushes[i].Outcome; got != want {
			t.Errorf("run %d outcome = %q, want %q", i, got, want)
		}
	}
	if got := done.Pushes[0]; got.Digest != "sha256:new" || got.Path != "/img/a.sqf" {
		t.Errorf("uploaded = %+v", got)
	}
	if got := done.Pushes[3]; got.Error != "network down" {
		t.Errorf("failed = %+v", got)
	}
}

// A project validate rejects is refused with the reasons in the document, and the exit is non-zero.
func TestProjectPushJSONPrintsWhyItWasRefused(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\n#DEP: env.img\nrun\n")
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "registry", "push", "--json")
	if err == nil {
		t.Fatal("push published a project validate rejects")
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Pushes        []any
		Problems      []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || len(doc.Pushes) != 0 || len(doc.Problems) == 0 || !strings.Contains(doc.Problems[0].Message+doc.Problems[len(doc.Problems)-1].Message, "env.img") {
		t.Errorf("document = %+v", doc)
	}
}
