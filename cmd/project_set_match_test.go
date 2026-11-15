package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

// set-match records the mode in the lock, and every later restore plans under it.
func TestProjectSelectMatchIsWhatRestoreUses(t *testing.T) {
	root := newProject(t)
	writeScript(t, root, "run.sh", "#DEP: star/2.7.11b\nrun\n")
	relative := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(relative)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	if out, err := run(t, "project", "set-match", "exact"); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("an unknown mode was accepted: %v\n%s", err, out)
	}
	if out, err := run(t, "project", "set-match", "identity"); err != nil {
		t.Fatalf("set-match failed: %v\n%s", err, out)
	}
	out, err := run(t, "project", "restore", "--dry-run", "--no-prebuilt", "--json")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	var report struct {
		Match string `json:"match"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.Match != "identity" {
		t.Errorf("match = %q, want the lock's", report.Match)
	}
}
