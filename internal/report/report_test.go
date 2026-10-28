package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

func write(t *testing.T, doc document) string {
	t.Helper()
	var out bytes.Buffer
	if err := Write(&out, doc); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A document with nothing in it still says its version, and writes its lists as [].
func TestWriteStampsTheVersionAndWritesEmptyListsAsArrays(t *testing.T) {
	got := write(t, &Steps{})
	want := "{\n  \"schema_version\": 1,\n  \"steps\": [],\n  \"problems\": []\n}\n"
	if got != want {
		t.Fatalf("document =\n%s\nwant\n%s", got, want)
	}
}

// Overlay and Location sit flat on a step, an empty optional
// field is left out, and requires_input is written even when false.
func TestStepIsOneFlatRecord(t *testing.T) {
	step := Step{
		Overlay:  Overlay{Name: "salmon/1.10.2", Type: artifact.TypeApp, Arch: "amd64"},
		Action:   ActionFetch,
		Endpoint: "ghcr.io/condatainer/cnt",
		Location: Location{Format: FormatSquashFS, Path: "/images/salmon--1.10.2.sqf", Layer: "user"},
		Outcome:  OutcomeDone,
	}
	encoded, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]any{
		"name": "salmon/1.10.2", "type": "app", "arch": "amd64", "action": "fetch",
		"endpoint": "ghcr.io/condatainer/cnt", "path": "/images/salmon--1.10.2.sqf",
		"layer": "user", "format": "squashfs", "outcome": "done", "requires_input": false,
	} {
		if fields[key] != want {
			t.Errorf("%s = %v, want %v", key, fields[key], want)
		}
	}
	for _, key := range []string{"runs", "digest", "identity", "equiv", "depends_on", "reason", "dir", "layout", "size", "job_id", "error"} {
		if _, ok := fields[key]; ok {
			t.Errorf("empty %s was written", key)
		}
	}
}

// A key is written in full, scheme and digest both.
func TestStepWritesKeysInFull(t *testing.T) {
	equiv := meta.KeyRef{Scheme: "definition-equiv-v1", SHA256: strings.Repeat("e9", 32)}
	got := write(t, &Steps{Steps: List[Step]{{Action: ActionBuild, Equiv: equiv}}})

	want := `"equiv": {
        "scheme": "definition-equiv-v1",
        "sha256": "` + equiv.SHA256 + `"
      }`
	if !strings.Contains(got, want) {
		t.Fatalf("document does not hold the full key:\n%s", got)
	}
}

func TestWriteDoesNotEscapeHTML(t *testing.T) {
	got := write(t, &Steps{Problems: List[Problem]{{Message: "logs go to run_<timestamp>.out & stay"}}})
	if !strings.Contains(got, "run_<timestamp>.out & stay") {
		t.Fatalf("message was escaped:\n%s", got)
	}
}

func TestFormatOfReadsTheExtension(t *testing.T) {
	for path, want := range map[string]Format{
		"a.sqf": FormatSquashFS, "a.sqsh": FormatSquashFS, "a.squashfs": FormatSquashFS,
		"env.img": FormatExt3, "env.ext3": FormatExt3,
		"base.sif": FormatSIF,
		"run.sh":   "",
	} {
		if got := FormatOf(path); got != want {
			t.Errorf("FormatOf(%q) = %q, want %q", path, got, want)
		}
	}
}
