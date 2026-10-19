package helper

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/catalog"
)

func TestHelperIndexNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "HTTP 404", err: errors.New("catalog: https://example.test/index/helpers.json: 404 Not Found"), want: true},
		{name: "local absence", err: &os.PathError{Op: "open", Path: "index/helpers.json", Err: os.ErrNotExist}, want: true},
		{name: "HTTP 403", err: errors.New("catalog: https://example.test/index/helpers.json: 403 Forbidden")},
		{name: "server error", err: errors.New("catalog: https://example.test/index/helpers.json: 500 Internal Server Error")},
		{name: "malformed index", err: errors.New("failed to parse helper index: invalid character")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := helperIndexNotFound(tt.err); got != tt.want {
				t.Errorf("helperIndexNotFound(%q) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

// A script is read from its source, written executable, and replaced on the next update.
func TestInstallScriptsReplacesWithExecutable(t *testing.T) {
	src := t.TempDir()
	script := filepath.Join(src, "helpers", "svc")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(t.Context(), []catalog.Spec{{Name: "lab", Base: src}}, catalog.Cache{})
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]RemoteScriptEntry{
		"svc":     {Path: "helpers/svc", Source: cat[0]},
		"missing": {Path: "helpers/missing", Source: cat[0]},
	}

	dest := t.TempDir()
	for _, body := range []string{"#!/bin/bash\necho one\n", "#!/bin/bash\necho two\n"} {
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		installScripts(t.Context(), entries, dest, &out)

		got, err := os.ReadFile(filepath.Join(dest, "svc"))
		if err != nil || string(got) != body {
			t.Fatalf("installed = %q, %v, want %q", got, err, body)
		}
		if info, _ := os.Stat(filepath.Join(dest, "svc")); info.Mode()&0o111 == 0 {
			t.Errorf("installed script is not executable: %v", info.Mode())
		}
		if !strings.Contains(out.String(), "WARN: failed to update missing") {
			t.Errorf("an unreadable script is not reported: %q", out.String())
		}
	}
}
