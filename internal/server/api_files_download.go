package server

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/utils/units"
)

// handleFSDownload serves GET /api/fs/download?path=...
//   - A file is served as is, following symlinks.
//   - A directory, or a symlink to one, is a zip streamed as it is built, never written to disk. Its entries are stored without compression. Inside it, a symlink to a file is included as that file's contents under the link's name, and a symlink to a folder is skipped. A zip is never refused for its size.
//   - The zip holds every folder as an entry, empty ones too. A single folder with nothing inside is not downloaded: the answer is 204.
//   - Repeating path streams all of them as one zip. Two paths with one base name are a 400.
//   - inline=1 serves a file with Content-Disposition: inline instead of attachment.
//   - gz=1 streams a file gzip-compressed as <name>.gz, and wins over inline.
//   - check=1 streams nothing, stops scanning if the client goes away, and answers {"ok":true,"empty":bool,"warn":"..."}.
//   - empty is true for a single folder with nothing inside.
//   - warn is not empty when the zip passes server.download_warn_size or server.download_warn_files. It reads "more than 5GB" or "more than 10000 files".
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

	// the dashboard pre-checks with check=1 to ask before a large zip and to
	// skip an empty folder; its <a download> trigger shows nothing itself
	if r.URL.Query().Get("check") == "1" {
		resp := map[string]any{"ok": true, "empty": false}
		if info.IsDir() {
			budget := newDownloadBudget()
			budget.addTree(r.Context(), path)
			resp["warn"] = budget.reason
			resp["empty"] = !dirHasContent(path)
		}
		writeJSON(w, resp)
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

// errOverWarning stops a size count once the zip passes a warning limit.
var errOverWarning = errors.New("download passes a warning limit")

// downloadBudget counts the files and bytes of a zip against the warning
// limits, and says which one was passed in reason.
type downloadBudget struct {
	maxFiles int
	maxMB    int64
	files    int
	bytes    int64
	reason   string
}

func newDownloadBudget() *downloadBudget {
	return &downloadBudget{maxFiles: keyDownloadWarnFiles.Get(), maxMB: keyDownloadWarnSize.Get()}
}

// add counts one file and returns errOverWarning once a limit is passed.
func (b *downloadBudget) add(size int64) error {
	b.files++
	b.bytes += size
	switch {
	case b.files > b.maxFiles:
		b.reason = fmt.Sprintf("more than %d files", b.maxFiles)
	case b.bytes > b.maxMB<<20:
		b.reason = "more than " + units.FormatMemoryDetail(b.maxMB)
	default:
		return nil
	}
	return errOverWarning
}

// walkRoot returns dirPath with a symlink at its root resolved, so a link to
// a directory is walked instead of skipped. Links below the root are not
// followed.
func walkRoot(dirPath string) string {
	if resolved, err := filepath.EvalSymlinks(dirPath); err == nil {
		return resolved
	}
	return dirPath
}

// zipFileInfo returns the info of the file d stands for when the zip holds it:
// a regular file, or a symlink to one (the target's info). A symlink to a
// folder, a broken link and other non-regular files are not held.
func zipFileInfo(p string, d fs.DirEntry) (os.FileInfo, bool) {
	if d.Type().IsRegular() {
		info, err := d.Info()
		return info, err == nil
	}
	if d.Type()&fs.ModeSymlink != 0 {
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return info, true
		}
	}
	return nil, false
}

// addTree counts the files the zip of dirPath holds (see zipFileInfo,
// unreadable entries skipped, matching the zip walk).
// It stops at the first limit passed, or when ctx is done.
func (b *downloadBudget) addTree(ctx context.Context, dirPath string) error {
	err := filepath.WalkDir(walkRoot(dirPath), func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ok := zipFileInfo(p, d); ok {
			return b.add(info.Size())
		}
		return nil
	})
	if errors.Is(err, errOverWarning) {
		return err
	}
	return nil
}

// dirHasContent reports whether the zip of dirPath holds a file or a folder
// below it (see zipFileInfo).
func dirHasContent(dirPath string) bool {
	root := walkRoot(dirPath)
	found := false
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil || p == root {
			return nil
		}
		if _, ok := zipFileInfo(p, d); ok || d.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// downloadDir zips dirPath and streams the archive with a Content-Disposition
// attachment header. Symlinks and other non-regular files inside the tree
// are skipped (symlinks are not followed). A folder with nothing inside
// answers 204.
func downloadDir(w http.ResponseWriter, dirPath string) {
	if !dirHasContent(dirPath) {
		w.WriteHeader(http.StatusNoContent)
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
// becomes a folder of its own name. check=1 only reports the warning.
func downloadMulti(w http.ResponseWriter, r *http.Request, paths []string) {
	type item struct {
		path string
		base string
		dir  bool
	}
	items := make([]item, 0, len(paths))
	seen := map[string]bool{}
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
	}

	if r.URL.Query().Get("check") == "1" {
		budget := newDownloadBudget()
		for _, it := range items {
			var err error
			if it.dir {
				err = budget.addTree(r.Context(), it.path)
			} else if info, serr := os.Stat(it.path); serr == nil {
				err = budget.add(info.Size())
			}
			if err != nil || r.Context().Err() != nil {
				break
			}
		}
		writeJSON(w, map[string]any{"ok": true, "empty": false, "warn": budget.reason})
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

// addTreeToZip adds dirPath to zw as the folder base: an entry for every
// folder, empty ones too, and one for every file (see zipFileInfo). A
// symlink to a folder is never followed, so a link back up the tree cannot
// loop. Unreadable entries are skipped.
func addTreeToZip(zw *zip.Writer, dirPath, base string) {
	dirPath = walkRoot(dirPath)
	filepath.WalkDir(dirPath, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dirPath, p)
		if err != nil {
			return nil
		}
		zipPath := filepath.ToSlash(filepath.Join(base, rel))
		if d.IsDir() {
			if info, err := d.Info(); err == nil {
				createZipEntry(zw, info, zipPath+"/")
			}
			return nil
		}
		if _, ok := zipFileInfo(p, d); ok {
			addFileToZip(zw, p, zipPath)
		}
		return nil
	})
}

// createZipEntry starts the entry name in zw, stored without compression and
// carrying info's modification time and permissions. A name ending in "/" is a
// folder. It returns nil when the entry cannot be started.
func createZipEntry(zw *zip.Writer, info os.FileInfo, name string) io.Writer {
	h, err := zip.FileInfoHeader(info)
	if err != nil {
		return nil
	}
	h.Name = name
	h.Method = zip.Store
	w, err := zw.CreateHeader(h)
	if err != nil {
		return nil
	}
	return w
}

// addFileToZip adds the file at path to zw as zipPath, skipping it when it
// cannot be read.
func addFileToZip(zw *zip.Writer, path, zipPath string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return
	}
	if w := createZipEntry(zw, info, zipPath); w != nil {
		io.Copy(w, f) //nolint:errcheck
	}
}
