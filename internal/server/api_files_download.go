package server

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

const (
	// maxDownloadEntries caps the number of files a directory download may
	// contain, checked by a pre-walk before any bytes are streamed.
	maxDownloadEntries = 20000
	// maxDownloadBytes caps the total uncompressed size a directory download
	// may contain, checked by the same pre-walk.
	maxDownloadBytes = 5 << 30 // 5 GiB
)

// handleFSDownload serves GET /api/fs/download?path=...
//   - A file is served as is, following symlinks.
//   - A directory is a streamed zip. Symlinks inside are skipped, and an oversized directory is rejected with 413.
//   - Repeating path streams all of them as one zip, with the same limits over the total. Two paths with one base name are a 400.
//   - inline=1 serves a file with Content-Disposition: inline instead of attachment.
//   - gz=1 streams a file gzip-compressed as <name>.gz, and wins over inline.
//   - check=1 only validates the download (200 or 413) and streams nothing.
func (s *srv) handleFSDownload(w http.ResponseWriter, r *http.Request) {
	paths := r.URL.Query()["path"]
	if len(paths) > 1 {
		downloadMulti(w, r, paths)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		fsErrorResponse(w, path, err)
		return
	}
	// FIFOs/sockets/devices are not downloadable — opening one can block forever.
	if !info.IsDir() && !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", http.StatusBadRequest)
		return
	}

	// the dashboard pre-checks with check=1 because its <a download>
	// trigger gets no error feedback from a failed real download
	if r.URL.Query().Get("check") == "1" {
		if info.IsDir() {
			if err := checkDirDownload(path); err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}

	if !info.IsDir() {
		if r.URL.Query().Get("gz") == "1" {
			downloadFileGz(w, path, info)
			return
		}
		inline := r.URL.Query().Get("inline") == "1"
		downloadFile(w, r, path, info, inline)
		return
	}

	downloadDir(w, path)
}

// downloadFile streams a single file, following symlinks. Served inline
// (the browser renders it if it can) when inline is true, as an attachment
// otherwise.
func downloadFile(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, inline bool) {
	f, err := os.Open(path)
	if err != nil {
		fsErrorResponse(w, path, err)
		return
	}
	defer f.Close()

	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, filepath.Base(path)))
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// downloadFileGz streams a single file gzip-compressed, as an attachment
// named <base>.gz. The gzip header carries the original name and mtime, so
// gunzip restores them.
func downloadFileGz(w http.ResponseWriter, path string, info os.FileInfo) {
	f, err := os.Open(path)
	if err != nil {
		fsErrorResponse(w, path, err)
		return
	}
	defer f.Close()

	base := filepath.Base(path)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".gz"))
	w.Header().Set("Content-Type", "application/gzip")

	gz := gzip.NewWriter(w)
	gz.Name = base
	gz.ModTime = info.ModTime()
	defer gz.Close()
	io.Copy(gz, f) //nolint:errcheck
}

// downloadBudget counts the files and bytes of a zip against
// maxDownloadEntries and maxDownloadBytes.
type downloadBudget struct {
	files int
	bytes int64
}

func (b *downloadBudget) add(size int64) error {
	b.files++
	if b.files > maxDownloadEntries {
		return fmt.Errorf("too large to download as zip: more than %d files", maxDownloadEntries)
	}
	b.bytes += size
	if b.bytes > maxDownloadBytes {
		return fmt.Errorf("too large to download as zip: over %d GiB in total", maxDownloadBytes>>30)
	}
	return nil
}

// addTree counts the regular files under dirPath (symlinks and other
// non-regular files skipped, matching the zip walk).
func (b *downloadBudget) addTree(dirPath string) error {
	return filepath.WalkDir(dirPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return b.add(info.Size())
	})
}

// checkDirDownload returns an error if dirPath exceeds the zip limits.
func checkDirDownload(dirPath string) error {
	var b downloadBudget
	return b.addTree(dirPath)
}

// downloadDir zips dirPath and streams the archive with a Content-Disposition
// attachment header. Symlinks and other non-regular files inside the tree
// are skipped (symlinks are not followed).
func downloadDir(w http.ResponseWriter, dirPath string) {
	if err := checkDirDownload(dirPath); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}

	base := filepath.Base(filepath.Clean(dirPath))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", base+".zip"))
	w.Header().Set("Content-Type", "application/zip")

	zw := zip.NewWriter(w)
	defer zw.Close()
	addTreeToZip(zw, dirPath, base)
}

// downloadMulti streams paths as one zip named after their parent
// directory. A file sits at the top of the archive and a directory
// becomes a folder of its own name. check=1 only validates.
func downloadMulti(w http.ResponseWriter, r *http.Request, paths []string) {
	type item struct {
		path string
		base string
		dir  bool
	}
	items := make([]item, 0, len(paths))
	seen := map[string]bool{}
	var budget downloadBudget
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fsErrorResponse(w, p, err)
			return
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			http.Error(w, fmt.Sprintf("%q is not a regular file", filepath.Base(p)), http.StatusBadRequest)
			return
		}
		base := filepath.Base(filepath.Clean(p))
		if seen[base] {
			http.Error(w, fmt.Sprintf("%q appears more than once", base), http.StatusBadRequest)
			return
		}
		seen[base] = true
		items = append(items, item{p, base, info.IsDir()})
		if info.IsDir() {
			err = budget.addTree(p)
		} else {
			err = budget.add(info.Size())
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
	}

	if r.URL.Query().Get("check") == "1" {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}

	name := filepath.Base(filepath.Dir(filepath.Clean(paths[0])))
	if name == "/" || name == "." {
		name = "download"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".zip"))
	w.Header().Set("Content-Type", "application/zip")

	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, it := range items {
		if it.dir {
			addTreeToZip(zw, it.path, it.base)
		} else {
			addFileToZip(zw, it.path, it.base)
		}
	}
}

// addTreeToZip adds the regular files under dirPath to zw below the folder
// base. Unreadable entries are skipped.
func addTreeToZip(zw *zip.Writer, dirPath, base string) {
	filepath.WalkDir(dirPath, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dirPath, p)
		if err != nil {
			return nil
		}
		addFileToZip(zw, p, filepath.ToSlash(filepath.Join(base, rel)))
		return nil
	})
}

// addFileToZip adds the file at path to zw as zipPath, skipping it when it
// cannot be read.
func addFileToZip(zw *zip.Writer, path, zipPath string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	entryWriter, err := zw.Create(zipPath)
	if err != nil {
		return
	}
	io.Copy(entryWriter, f) //nolint:errcheck
}
