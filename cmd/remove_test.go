package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
)

// --json never prompts, so an unanswered prompt is a no: it reports, and -y
// makes it act. Text output keeps its prompt.
func TestPreviewOnly(t *testing.T) {
	defer func(old bool) { utils.YesMode = old }(utils.YesMode)

	utils.YesMode = false
	if !previewOnly(true) {
		t.Error("--json acted with no -y")
	}
	if previewOnly(false) {
		t.Error("text output was a preview")
	}
	utils.YesMode = true
	if previewOnly(true) {
		t.Error("--json -y only previewed")
	}
}

// A removal takes the file and its .env sidecar; a preview only checks that it
// could and leaves both.
func TestRemoveOne(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool--1.sqf")
	for _, p := range []string{file, file + ".env"} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if reason, err := removeOne(removalItem{name: "tool/1", path: file}, true); reason != "" || err != nil {
		t.Fatalf("preview = %q, %v", reason, err)
	}
	if !utils.FileExists(file) || !utils.FileExists(file+".env") {
		t.Fatal("a preview removed a file")
	}
	if reason, err := removeOne(removalItem{name: "tool/1", path: file}, false); reason != "" || err != nil {
		t.Fatalf("removal = %q, %v", reason, err)
	}
	if utils.FileExists(file) || utils.FileExists(file+".env") {
		t.Error("the file or its sidecar is still there")
	}
}

func TestRemoveOneKeepsWhatIsInAReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool--1.sqf")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755) //nolint:errcheck
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}

	if reason, _ := removeOne(removalItem{name: "tool/1", path: file}, false); reason != report.KeptProtected {
		t.Errorf("reason = %q, want protected", reason)
	}
	if !utils.FileExists(file) {
		t.Error("a protected file was removed")
	}
}

// A preview writes would_remove, a real run removed. A name that was never
// installed is not a failure, but one that could not be removed is.
func TestFinishOverlayRemoval(t *testing.T) {
	defer func(j, d bool) { removeJSON, removePreview = j, d }(removeJSON, removePreview)
	removed := []report.Removed{{Overlay: report.Overlay{Name: "tool/1"}}}
	cmd := &cobra.Command{}

	for _, tc := range []struct {
		preview bool
		want    string
	}{{false, "removed"}, {true, "would_remove"}} {
		removeJSON, removePreview = true, tc.preview
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := finishOverlayRemoval(cmd, removed, nil, false); err != nil {
			t.Fatal(err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if _, ok := doc[tc.want]; !ok || len(doc) != 4 {
			t.Errorf("preview=%v: keys = %v, want %q", tc.preview, keysOf(doc), tc.want)
		}
	}

	removeJSON, removePreview = true, false
	cmd.SetOut(&bytes.Buffer{})
	if err := finishOverlayRemoval(cmd, nil, []report.Kept{{Reason: report.KeptNotFound}}, false); err != nil {
		t.Errorf("a name that was not installed failed the command: %v", err)
	}
	if err := finishOverlayRemoval(cmd, nil, []report.Kept{{Reason: report.KeptInUse}}, false); err == nil {
		t.Error("an overlay in use did not fail the command")
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
