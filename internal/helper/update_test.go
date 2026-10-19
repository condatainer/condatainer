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
// A file without a #! line is reported and leaves any installed copy alone.
func TestInstallScriptsReplacesWithExecutable(t *testing.T) {
	src := t.TempDir()
	script := filepath.Join(src, "helpers", "svc")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "helpers", "data"), []byte("\x00not a script"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(t.Context(), []catalog.Spec{{Name: "lab", Base: src}}, catalog.Cache{})
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]RemoteScriptEntry{
		"svc":     {Path: "helpers/svc", Source: cat[0]},
		"data":    {Path: "helpers/data", Source: cat[0]},
		"missing": {Path: "helpers/missing", Source: cat[0]},
	}

	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "data"), []byte("#!/bin/bash\nold\n"), 0o755); err != nil {
		t.Fatal(err)
	}
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
		if !strings.Contains(out.String(), "WARN: failed to update data: helper: not a script") {
			t.Errorf("a file without #! is not reported: %q", out.String())
		}
		if kept, _ := os.ReadFile(filepath.Join(dest, "data")); string(kept) != "#!/bin/bash\nold\n" {
			t.Errorf("an installed copy was overwritten by a non-script: %q", kept)
		}
	}
}

// A directory source offers the files in helpers/, with no index to write.
func TestReadHelperIndexScansDirectorySource(t *testing.T) {
	src := t.TempDir()
	for name, body := range map[string]string{
		"helpers/svc":       "#!/bin/bash\n",
		"helpers/.hidden":   "x",
		"helpers/sub/inner": "x",
		"recipes/foo":       "#DESC:x\n",
	} {
		path := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.Open(t.Context(), []catalog.Spec{{Name: "lab", Base: src}}, catalog.Cache{})
	if err != nil {
		t.Fatal(err)
	}

	got, err := readHelperIndex(t.Context(), cat, cat[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["svc"].Path != "helpers/svc" {
		t.Errorf("helpers = %+v, want only svc at helpers/svc", got)
	}

	if err := os.RemoveAll(filepath.Join(src, "helpers")); err != nil {
		t.Fatal(err)
	}
	if got, err := readHelperIndex(t.Context(), cat, cat[0], nil); err != nil || len(got) != 0 {
		t.Errorf("without helpers/: %+v, %v, want none and no error", got, err)
	}
}
