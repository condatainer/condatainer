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

// A missing copy has its recipe's key and tag and nothing of a manifest; a
// published one adds the digest and identity.
func TestCopyAddsTheManifestOnlyWhenPublished(t *testing.T) {
	equiv := meta.KeyRef{Scheme: "definition-equiv-v1", SHA256: "e90d"}
	missing, err := json.Marshal(Copy{Overlay: Overlay{Name: "ubuntu24/base", Type: artifact.TypeOS, Arch: "amd64"},
		Equiv: equiv, Tag: "ubuntu24--base__eq-e90d__amd64"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(missing, &m); err != nil {
		t.Fatal(err)
	}
	if m["published"] != false || m["tag"] == nil || m["equiv"] == nil || m["digest"] != nil || m["identity"] != nil {
		t.Errorf("missing copy = %s", missing)
	}

	published, err := json.Marshal(Copy{Published: true, Equiv: equiv, Digest: "sha256:ab",
		Identity: meta.KeyRef{Scheme: "definition-identity-v1", SHA256: "9f2c"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(published), `"digest":"sha256:ab"`) || !strings.Contains(string(published), `"identity":{"scheme":"definition-identity-v1","sha256":"9f2c"}`) {
		t.Errorf("published copy = %s", published)
	}
}
