package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/restore"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/spf13/pflag"
)

// --dry-run reports the plan and acquires nothing. The project below has no
// images at all, so every step is work and none of it may happen.
func TestProjectRestoreDryRunAcquiresNothing(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	relative := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(relative)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	before := lockBytes(t, root)
	if out, err := run(t, "project", "restore", "--dry-run", "--no-prebuilt"); err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(entry.Name(), ".sqf") {
			t.Errorf("a dry run produced %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if after := lockBytes(t, root); after != before {
		t.Error("a dry run rewrote the lock")
	}
}

func TestProjectRestoreDryRunJSONListsTheSteps(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	relative := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(relative)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "restore", "--dry-run", "--no-prebuilt", "--json")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	var report struct {
		SchemaVersion int    `json:"schema_version"`
		Root          string `json:"root"`
		Problems      []any  `json:"problems"`
		Steps         []struct {
			Name     string `json:"name"`
			Action   string `json:"action"`
			Artifact string `json:"artifact"`
			Direct   bool   `json:"direct"`
			Identity struct {
				Scheme string `json:"scheme"`
				SHA256 string `json:"sha256"`
			} `json:"identity"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.SchemaVersion != 1 || report.Root != root || report.Problems == nil {
		t.Errorf("document = %+v", report)
	}
	if len(report.Steps) != 1 || report.Steps[0].Action != "build" {
		t.Fatalf("steps = %#v", report.Steps)
	}
	if step := report.Steps[0]; step.Artifact != relative || !step.Direct || step.Identity.Scheme == "" || step.Identity.SHA256 == "" {
		t.Errorf("step = %+v, want its vendored directory and full locked identity", step)
	}
}

// An unsound lock stops restore before it acquires anything, and says why.
func TestProjectRestoreFailsOnAnInvalidLock(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf("provenance/star--2.7.11b@000000000000")
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if out, err := run(t, "project", "restore", "--no-prebuilt"); err == nil {
		t.Fatalf("restore accepted an invalid lock: %s", out)
	}
}

// An equivalent artifact stands in, and the note says which inputs differ. The
// field names hold colons themselves, so only the colon-and-space ends one.
func TestEquivalentNoteNamesTheInputsThatDiffer(t *testing.T) {
	result := restore.Result{
		Identity: meta.KeyRef{Scheme: "identity-v1", SHA256: "41ab1c2d3e4f5a6b7c8d"},
		Found:    meta.KeyRef{Scheme: "identity-v1", SHA256: "9f2b7e04a1c3d5e6f708"},
		Diffs: []string{
			"src:gtf: 9f2b7e04a1c3 -> 3c81d5e2b7a4",
			"dep:samtools/1.23.1: 11aa22bb33cc -> 44dd55ee66ff",
		},
	}
	got := equivalentNote(result)
	for _, want := range []string{"equivalent, not 41ab1c2d3e4f;", "differs: src:gtf, dep:samtools/1.23.1"} {
		if !strings.Contains(got, want) {
			t.Errorf("note = %q, want it to contain %q", got, want)
		}
	}
	// Digests are for --json, not a restore of many artifacts.
	if strings.Contains(got, "3c81d5e2b7a4") {
		t.Errorf("note = %q carries a digest", got)
	}
}

// With no diffs to name, the note is what it was before diffs existed.
func TestEquivalentNoteWithoutDiffsNamesNothing(t *testing.T) {
	got := equivalentNote(restore.Result{Identity: meta.KeyRef{Scheme: "identity-v1", SHA256: "41ab1c2d3e4f5a6b7c8d"}})
	if want := " (equivalent, not 41ab1c2d3e4f)"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

// A flag nobody classified is one a submitted rebuild might silently drop.
func TestEveryRestoreFlagHasAJobRule(t *testing.T) {
	cmd := newProjectRestoreCmd()
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if _, ok := restoreJobRules[f.Name]; !ok && f.Name != "help" {
			t.Errorf("--%s has no entry in restoreJobRules: say whether a submitted rebuild repeats it", f.Name)
		}
	})
	for name := range restoreJobRules {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("restoreJobRules lists --%s, which restore does not have", name)
		}
	}
}

// Each outcome of a run lands on its own step, and a path pin is told apart by its path.
func TestRestoreDocMergesTheRunIntoThePlan(t *testing.T) {
	key := func(hex string) meta.KeyRef { return meta.KeyRef{Scheme: "identity-v1", SHA256: hex} }
	plan := &restore.Plan{Root: "/p", Match: restore.MatchEquivalence, Steps: []restore.Step{
		{Artifact: "a", Name: "a", Identity: key("aa"), Action: restore.ActionBuild},
		{Artifact: "b", Name: "b", Identity: key("bb"), Action: restore.ActionBuild, DependsOn: []string{"a"}},
		{Artifact: "c", Name: "c", Identity: key("cc"), Action: restore.ActionFetch},
		{Artifact: "d", Name: "d", Identity: key("dd"), Action: restore.ActionBuild},
		{Artifact: "e", Name: "e", Identity: key("ee"), Action: restore.ActionAdopt, Destination: "x.sqf"},
		{Artifact: "e", Name: "e", Identity: key("ee"), Action: restore.ActionAdopt, Destination: "y.sqf"},
	}}
	run := &restore.Report{Plan: plan,
		Results: []restore.Result{
			{Artifact: "c", Name: "c", Identity: key("cc"), Found: key("c2"), Outcome: restore.OutcomeBuilt, Path: "/img/c.sqf"},
			{Artifact: "e", Name: "e", Identity: key("ee"), Outcome: restore.OutcomeAdopted, Destination: "y.sqf", Path: "/p/y.sqf"},
		},
		Failures:  []restore.Failure{{Artifact: "a", Name: "a", Reason: "boom"}},
		Blocked:   []restore.Blocked{{Artifact: "b", Name: "b", Cause: "a"}},
		Submitted: []restore.Submitted{{Artifact: "d", Name: "d", JobID: "42", DependsOn: []string{"7"}}},
	}

	doc := restoreDoc("/p", lock.New(), plan, run)
	if doc.Root != "/p" || doc.Match != "equivalence" {
		t.Errorf("root, match = %q, %q", doc.Root, doc.Match)
	}
	want := []struct {
		outcome report.Outcome
		action  report.Action
	}{
		{report.OutcomeFailed, report.ActionBuild},
		{report.OutcomeSkipped, report.ActionBuild},
		{report.OutcomeDone, report.ActionBuild},
		{report.OutcomeSubmitted, report.ActionBuild},
		{report.OutcomeSkipped, report.ActionAdopt},
		{report.OutcomeDone, report.ActionAdopt},
	}
	if len(doc.Steps) != len(want) {
		t.Fatalf("steps = %d, want %d", len(doc.Steps), len(want))
	}
	for i, w := range want {
		step := doc.Steps[i]
		if step.Outcome != w.outcome || step.Action != w.action {
			t.Errorf("step %d = %s/%s, want %s/%s", i, step.Action, step.Outcome, w.action, w.outcome)
		}
	}
	if got := doc.Steps[4]; got.Reason != "not reached" {
		t.Errorf("unreached step reason = %q", got.Reason)
	}
	// The fetch fell back to a build, and a stand-in is reported with its full key.
	if got := doc.Steps[2]; got.Found != key("c2") || got.Path != "/img/c.sqf" {
		t.Errorf("fetched-then-built step = %+v", got)
	}
	if got := doc.Steps[0]; got.Error != "boom" {
		t.Errorf("failed step error = %q", got.Error)
	}
	if got := doc.Steps[1]; got.Cause != "a" || got.Reason == "" {
		t.Errorf("blocked step = %+v", got)
	}
	if got := doc.Steps[3]; got.JobID != "42" || got.Runs != report.RunsJob || len(got.WaitsFor) != 1 {
		t.Errorf("submitted step = %+v", got)
	}
}

// A run that planning stopped has no outcomes, and a dry run has none either.
func TestRestoreDocHasNoOutcomeBeforeAnythingRan(t *testing.T) {
	plan := &restore.Plan{Root: "/p", Steps: []restore.Step{{Artifact: "a", Name: "a", Action: restore.ActionBuild}},
		Problems: []string{"x cannot be built"}}
	for name, run := range map[string]*restore.Report{"dry run": nil, "stopped": {Plan: plan, Problems: plan.Problems}} {
		doc := restoreDoc("/p", lock.New(), plan, run)
		if doc.Steps[0].Outcome != "" || len(doc.Problems) != 1 {
			t.Errorf("%s: step = %+v, problems = %v", name, doc.Steps[0], doc.Problems)
		}
	}
}

func lockBytes(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(lock.FilePath(root))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
