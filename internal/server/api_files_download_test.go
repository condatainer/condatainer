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

	"github.com/condatainer/condatainer/internal/settings/settingstest"
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
		if f.Method != zip.Store {
			t.Errorf("%s: method = %d, want stored (no compression)", f.Name, f.Method)
		}
		if f.Modified.IsZero() {
			t.Errorf("%s: no modification time", f.Name)
		}
	}
	sort.Strings(names)
	if want := []string{"a.txt", "d/", "d/sub/", "d/sub/b.txt"}; !slices.Equal(names, want) {
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

// TestDownloadSymlinkedDir checks that a symlink to a directory downloads
// the directory's files, alone and in a multi-path request.
func TestDownloadSymlinkedDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "o.txt")
	if err := os.WriteFile(other, []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{
		"path=" + url.QueryEscape(link),
		"path=" + url.QueryEscape(link) + "&path=" + url.QueryEscape(other),
	} {
		w := httptest.NewRecorder()
		(&srv{}).handleFSDownload(w, httptest.NewRequest(http.MethodGet, "/api/fs/download?"+query, nil))
		zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		found := false
		for _, f := range zr.File {
			found = found || f.Name == "link/f.txt"
		}
		if !found {
			t.Fatalf("%s: zip has no link/f.txt", query)
		}
	}
}

// TestDownloadEmptyFolders checks that a lone empty folder is not
// downloaded (check=1 says so and a real request answers 204), while an
// empty folder in a multi-path request, or inside a folder, is a zip entry.
func TestDownloadEmptyFolders(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"empty", "outer/inner"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		(&srv{}).handleFSDownload(w, httptest.NewRequest(http.MethodGet, "/api/fs/download?"+query, nil))
		return w
	}
	names := func(w *httptest.ResponseRecorder) []string {
		t.Helper()
		zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range zr.File {
			out = append(out, f.Name)
		}
		sort.Strings(out)
		return out
	}
	p := func(name string) string { return "path=" + url.QueryEscape(filepath.Join(root, name)) }

	if w := get(p("empty") + "&check=1"); !strings.Contains(w.Body.String(), `"empty":true`) {
		t.Fatalf("check of an empty folder = %s, want empty:true", w.Body.String())
	}
	if w := get(p("empty")); w.Code != http.StatusNoContent {
		t.Fatalf("empty folder: status = %d, want 204", w.Code)
	}
	if w := get(p("outer") + "&check=1"); !strings.Contains(w.Body.String(), `"empty":false`) {
		t.Fatalf("check of a folder with a subfolder = %s, want empty:false", w.Body.String())
	}
	if got, want := names(get(p("outer"))), []string{"outer/", "outer/inner/"}; !slices.Equal(got, want) {
		t.Fatalf("folder with an empty subfolder: entries = %v, want %v", got, want)
	}
	if got, want := names(get(p("empty")+"&"+p("a.txt"))), []string{"a.txt", "empty/"}; !slices.Equal(got, want) {
		t.Fatalf("empty folder with a file: entries = %v, want %v", got, want)
	}
}

// TestDownloadWarning checks that a zip past server.download_warn_files or
// server.download_warn_size is reported by check=1 and still downloads, alone
// and in a multi-path request.
func TestDownloadWarning(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"d/1", "d/2", "d/3"} {
		if err := os.WriteFile(filepath.Join(root, n), bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		(&srv{}).handleFSDownload(w, httptest.NewRequest(http.MethodGet, "/api/fs/download?"+query, nil))
		return w
	}
	one := "path=" + url.QueryEscape(filepath.Join(root, "d"))
	multi := one + "&path=" + url.QueryEscape(filepath.Join(root, "d", "1"))

	for _, q := range []string{one, multi} {
		if w := get(q + "&check=1"); strings.Contains(w.Body.String(), `"warn":"more`) {
			t.Fatalf("%s: warned under the default limits: %s", q, w.Body.String())
		}
	}

	settingstest.Override(t, "server.download_warn_files", "2")
	for _, q := range []string{one, multi} {
		if w := get(q + "&check=1"); !strings.Contains(w.Body.String(), `"warn":"more than 2 files"`) {
			t.Fatalf("%s: check = %s, want a files warning", q, w.Body.String())
		}
		if w := get(q); w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want the download to go ahead", q, w.Code)
		}
	}

	settingstest.Override(t, "server.download_warn_files", "100")
	settingstest.Override(t, "server.download_warn_size", "2m")
	if w := get(one + "&check=1"); !strings.Contains(w.Body.String(), `"warn":"more than 2MB"`) {
		t.Fatalf("check = %s, want a size warning", w.Body.String())
	}
}

// TestDownloadFolderSymlinks checks that a symlink to a file inside a folder
// is zipped as that file's contents under the link's name, that a symlink to
// a folder (a link back up the tree included) is skipped and cannot loop, and
// that following a folder link at the top level zips its target once.
func TestDownloadFolderSymlinks(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "outside"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "results"), 0o755))
	must(os.WriteFile(filepath.Join(root, "outside", "data.bin"), []byte("payload"), 0o644))
	must(os.WriteFile(filepath.Join(root, "results", "real.txt"), []byte("r"), 0o644))
	must(os.Symlink(filepath.Join("..", "outside", "data.bin"), filepath.Join(root, "results", "data.bin")))
	must(os.Symlink("..", filepath.Join(root, "results", "up")))
	must(os.Symlink(filepath.Join("..", "outside"), filepath.Join(root, "results", "raw")))
	must(os.Symlink("missing", filepath.Join(root, "results", "broken")))

	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		(&srv{}).handleFSDownload(w, httptest.NewRequest(http.MethodGet, "/api/fs/download?"+query, nil))
		return w
	}
	entries := func(w *httptest.ResponseRecorder) map[string]string {
		t.Helper()
		zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			out[f.Name] = string(b)
		}
		return out
	}
	p := func(name string) string { return "path=" + url.QueryEscape(filepath.Join(root, name)) }

	got := entries(get(p("results")))
	if len(got) != 3 || got["results/real.txt"] != "r" || got["results/data.bin"] != "payload" {
		t.Fatalf("entries = %v, want results/, results/real.txt and results/data.bin holding the target's contents", got)
	}

	if w := get(p("results") + "&check=1"); !strings.Contains(w.Body.String(), `"empty":false`) {
		t.Fatalf("check = %s", w.Body.String())
	}
	settingstest.Override(t, "server.download_warn_files", "1")
	if w := get(p("results") + "&check=1"); !strings.Contains(w.Body.String(), `"warn":"more than 1 files"`) {
		t.Fatalf("the linked file is not counted: %s", w.Body.String())
	}

	// A link back up the tree, followed once at the top level, is finite.
	up := entries(get(p("results/up")))
	if up["up/results/real.txt"] != "r" || up["up/results/data.bin"] != "payload" {
		t.Fatalf("the target of the top-level link is not zipped: %v", up)
	}
	for name := range up {
		if strings.Contains(name, "/up/") || strings.Contains(name, "/raw/") {
			t.Fatalf("a folder link below the top was followed: %s", name)
		}
	}

	// A folder holding only a link to a file is not empty.
	must(os.MkdirAll(filepath.Join(root, "onlylink"), 0o755))
	must(os.Symlink(filepath.Join("..", "outside", "data.bin"), filepath.Join(root, "onlylink", "d")))
	settingstest.Override(t, "server.download_warn_files", "100")
	if w := get(p("onlylink") + "&check=1"); !strings.Contains(w.Body.String(), `"empty":false`) {
		t.Fatalf("a folder with a file link counted as empty: %s", w.Body.String())
	}
}
