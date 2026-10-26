package helper

import (
	"crypto/sha256"
	"encoding/hex"
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

// A directory source offers the files in helpers/, with no index to write.
func TestReadHelperIndexScansDirectorySource(t *testing.T) {
	src := t.TempDir()
	for name, body := range map[string]string{
		"helpers/svc":       "#!/bin/bash\n#DESC: a service ## note\n",
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
	sum := sha256.Sum256([]byte("#!/bin/bash\n#DESC: a service ## note\n"))
	if e := got["svc"]; e.Description != "a service" || e.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("svc = %+v, want its description and the hash of its bytes", e)
	}

	if err := os.RemoveAll(filepath.Join(src, "helpers")); err != nil {
		t.Fatal(err)
	}
	if got, err := readHelperIndex(t.Context(), cat, cat[0], nil); err != nil || len(got) != 0 {
		t.Errorf("without helpers/: %+v, %v, want none and no error", got, err)
	}
}

func TestHelperIndexEntryNeedsAHash(t *testing.T) {
	good := `{"schema_version": 1, "helpers": {"a": {"path": "helpers/a", "sha256": "ab"}}}`
	if got, err := decodeHelperIndex([]byte(good)); err != nil || got["a"].SHA256 != "ab" || got["a"].Description != "" {
		t.Errorf("an entry with no description: %+v, %v", got, err)
	}
	bad := `{"schema_version": 1, "helpers": {"a": {"path": "helpers/a", "sha256": "ab"}, "b": {"path": "helpers/b"}}}`
	if _, err := decodeHelperIndex([]byte(bad)); !errors.Is(err, ErrIndexEntry) || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("an entry with no sha256: %v", err)
	}
}
