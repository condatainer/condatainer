package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

// The listing reads the lock and the records beside it, and is the only view
// that shows a manual pin — no scan produces one.
func TestProjectListShowsEveryPinAndMarksTheManualOne(t *testing.T) {
	root := newProject(t)
	declared := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	manual := vendorArtifact(t, root, "ubuntu24/build-essential", "echo apt\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = pinOf(declared)
	l.Pins["ubuntu24/build-essential"] = manualPinOf(manual)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	rows, err := run(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for _, want := range []string{"star/2.7.11b", "ubuntu24/build-essential", "manual"} {
		if !strings.Contains(rows, want) {
			t.Errorf("stdout does not carry %q:\n%s", want, rows)
		}
	}
	// A named pin's key is its name, so the listing must not print it twice.
	if strings.Contains(rows, "star/2.7.11b (star/2.7.11b)") {
		t.Errorf("the name was repeated after the key it equals:\n%s", rows)
	}
	console, err := runConsole(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(console, "2 pin(s), 1 manual") {
		t.Errorf("the summary is not on the console:\n%s", console)
	}
}

// JSON carries the flag as data, never as the styled word the text view prints.
func TestProjectListJSONReportsTheManualFlag(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "ubuntu24/build-essential", "echo apt\n")
	l := lock.New()
	l.Pins["ubuntu24/build-essential"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "list", "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v", err)
	}
	var report struct {
		Pins []struct {
			Request  string `json:"request"`
			Name     string `json:"name"`
			Type     string `json:"type"`
			Identity struct {
				SHA256 string `json:"sha256"`
			} `json:"identity"`
			Manual bool `json:"manual"`
		} `json:"pins"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("cannot decode: %v\n%s", err, out)
	}
	if len(report.Pins) != 1 {
		t.Fatalf("pins = %#v", report.Pins)
	}
	pin := report.Pins[0]
	if !pin.Manual || pin.Name != "ubuntu24/build-essential" || pin.Identity.SHA256 == "" || pin.Type == "" {
		t.Errorf("pin = %#v", pin)
	}
}

// A pin whose artifact is gone is still listed: the key is what addresses it,
// and hiding it would hide the thing to unpin.
func TestProjectListReportsAnUnreadableArtifact(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(lock.Dir(root), artifact)); err != nil {
		t.Fatal(err)
	}

	rows, err := run(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(rows, "star/2.7.11b") || !strings.Contains(rows, "unreadable") {
		t.Errorf("a broken pin was not listed as one:\n%s", rows)
	}
}

// In JSON the broken pin is listed too, and the reason is a problem named by the pin.
func TestProjectListJSONReportsAnUnreadableArtifactAsAProblem(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "star/2.7.11b", "echo star\n")
	l := lock.New()
	l.Pins["star/2.7.11b"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(lock.Dir(root), artifact)); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "project", "list", "--json")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Pins          []struct{ Request string }
		Problems      []struct{ Name, Message string }
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || len(doc.Pins) != 1 || len(doc.Problems) != 1 || doc.Problems[0].Name != "star/2.7.11b" {
		t.Errorf("document = %+v", doc)
	}
}

// A path key addresses a file and says nothing about what is in it, so the name
// is printed after it — the one case where the second column carries something.
func TestProjectListNamesWhatAPathPinHolds(t *testing.T) {
	root := newProject(t)
	artifact := vendorArtifact(t, root, "combined/1.0", "echo combined\n")
	l := lock.New()
	l.Pins["path:overlays/combined.sqf"] = manualPinOf(artifact)
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}

	rows, err := run(t, "project", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(rows, "path:overlays/combined.sqf (combined/1.0)") {
		t.Errorf("a path pin does not name its artifact:\n%s", rows)
	}
}
