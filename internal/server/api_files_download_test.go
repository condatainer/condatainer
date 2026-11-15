package server

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestDownloadGz checks that gz=1 streams a single file gzip-compressed,
// named <base>.gz, and that it decompresses back to the original content.
func TestDownloadGz(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("condatainer\n"), 1000)
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet,
		"/api/fs/download?path="+url.QueryEscape(path)+"&gz=1", nil)
	w := httptest.NewRecorder()
	(&srv{}).handleFSDownload(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `"notes.txt.gz"`) {
		t.Fatalf("Content-Disposition = %q, want notes.txt.gz attachment", cd)
	}
	if len(w.Body.Bytes()) >= len(content) {
		t.Fatalf("compressed body (%d bytes) not smaller than original (%d bytes)", w.Body.Len(), len(content))
	}

	gr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Name != "notes.txt" {
		t.Fatalf("gzip header name = %q, want notes.txt", gr.Name)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("decompressed content mismatch: %d bytes, want %d", len(got), len(content))
	}
}

// TestDownloadMulti checks that repeated path streams one zip holding each
// file at the top and each directory as a folder, and that check=1 and a
// duplicate base name answer without streaming.
func TestDownloadMulti(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "d", "sub"), 0o755))
	must(os.WriteFile(filepath.Join(root, "a.txt"), []byte("aaa"), 0o644))
	must(os.WriteFile(filepath.Join(root, "d", "sub", "b.txt"), []byte("bbb"), 0o644))

	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		(&srv{}).handleFSDownload(w, httptest.NewRequest(http.MethodGet, "/api/fs/download?"+query, nil))
		return w
	}
	q := "path=" + url.QueryEscape(filepath.Join(root, "a.txt")) + "&path=" + url.QueryEscape(filepath.Join(root, "d"))

	w := get(q)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `"`+filepath.Base(root)+`.zip"`) {
		t.Fatalf("Content-Disposition = %q, want <parent>.zip", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	if want := []string{"a.txt", "d/sub/b.txt"}; !slices.Equal(names, want) {
		t.Fatalf("zip entries = %v, want %v", names, want)
	}

	if w := get(q + "&check=1"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("check=1: status %d, body %s", w.Code, w.Body.String())
	}
	dup := "path=" + url.QueryEscape(filepath.Join(root, "a.txt")) + "&path=" + url.QueryEscape(filepath.Join(root, "a.txt"))
	if w := get(dup); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate base: status = %d, want 400", w.Code)
	}
}
