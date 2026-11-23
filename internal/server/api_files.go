package server

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// fsErrorResponse writes err for path as a human-readable message with a
// fitting status (403 permission, 404 missing, 400 otherwise).
func fsErrorResponse(w http.ResponseWriter, path string, err error) {
	switch {
	case os.IsPermission(err):
		http.Error(w, "no permission to access "+path, http.StatusForbidden)
	case os.IsNotExist(err):
		http.Error(w, path+" does not exist", http.StatusNotFound)
	case errors.Is(err, syscall.ENOTDIR):
		http.Error(w, path+" is not a directory", http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// maxFSEntries is the maximum number of directory entries returned per request.
// Prevents loading thousands of entries from large shared dirs like /home or /scratch.
const maxFSEntries = 2000

// handleFS serves GET /api/fs?path= — directory listing for the file
// browser — DELETE /api/fs?path= — deleting a file or directory (see
// handleFSDelete in api_files_delete.go) — and PATCH /api/fs?path= —
// renaming or moving a file or directory (see handleFSRename in
// api_files_move.go). Sets X-FS-Truncated: true when the directory
// exceeds maxFSEntries entries.
func (s *srv) handleFS(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		s.handleFSDelete(w, r)
		return
	case http.MethodPatch:
		s.handleFSRename(w, r)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		if home, err := utils.RealHome(); err == nil {
			path = home
		} else {
			path = "/"
		}
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		fsErrorResponse(w, path, err)
		return
	}

	if len(entries) > maxFSEntries {
		entries = entries[:maxFSEntries]
		w.Header().Set("X-FS-Truncated", "true")
	}

	type fsEntry struct {
		Name       string    `json:"name"`
		IsDir      bool      `json:"is_dir"`
		Path       string    `json:"path"`
		Size       int64     `json:"size,omitempty"`
		ModifiedAt time.Time `json:"modified_at,omitempty"`
		Mode       string    `json:"mode,omitempty"`  // symbolic permissions, e.g. "drwxr-x---"
		Owner      string    `json:"owner,omitempty"` // owner name, or the numeric uid when it has no name
		Group      string    `json:"group,omitempty"` // group name, or the numeric gid when it has no name
	}
	ids := &idNames{users: map[string]string{}, groups: map[string]string{}}
	fill := func(fe *fsEntry, info fs.FileInfo) {
		fe.Size = info.Size()
		fe.ModifiedAt = info.ModTime()
		fe.Mode = lsMode(info.Mode())
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			fe.Owner = ids.user(st.Uid)
			fe.Group = ids.group(st.Gid)
		}
	}
	// Each entry needs a stat for size/mtime (and a follow for symlinks) —
	// on a network filesystem those are per-entry round trips, so they run
	// concurrently; done serially, a full listing takes many seconds.
	out := make([]fsEntry, len(entries))
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for i, e := range entries {
		out[i] = fsEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Path:  filepath.Join(path, e.Name()),
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(fe *fsEntry, e os.DirEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			// For symlinks, follow the link to determine if the target is a directory.
			if e.Type()&os.ModeSymlink != 0 {
				if info, err := os.Stat(fe.Path); err == nil {
					fe.IsDir = info.IsDir()
					fill(fe, info)
				}
			} else if info, err := e.Info(); err == nil {
				fill(fe, info)
			}
		}(&out[i], e)
	}
	wg.Wait()
	writeJSON(w, out)
}

// idNames resolves uids and gids to names, looking each id up once per
// listing. An id without a name stays numeric.
type idNames struct {
	mu     sync.Mutex
	users  map[string]string
	groups map[string]string
}

func (n *idNames) user(uid uint32) string {
	return n.resolve(n.users, uid, func(id string) (string, error) {
		u, err := user.LookupId(id)
		if err != nil {
			return "", err
		}
		return u.Username, nil
	})
}

func (n *idNames) group(gid uint32) string {
	return n.resolve(n.groups, gid, func(id string) (string, error) {
		g, err := user.LookupGroupId(id)
		if err != nil {
			return "", err
		}
		return g.Name, nil
	})
}

func (n *idNames) resolve(cache map[string]string, id uint32, lookup func(string) (string, error)) string {
	key := strconv.FormatUint(uint64(id), 10)
	n.mu.Lock()
	defer n.mu.Unlock()
	if name, ok := cache[key]; ok {
		return name
	}
	name, err := lookup(key)
	if err != nil || name == "" {
		name = key
	}
	cache[key] = name
	return name
}

// lsMode formats m as ls -l does: a type character and three rwx triples.
//   - setuid and setgid show as s (S without the execute bit) in the user and group triples.
//   - sticky shows as t (T without the execute bit) in the other triple.
func lsMode(m fs.FileMode) string {
	b := []byte("----------")
	switch {
	case m&fs.ModeDir != 0:
		b[0] = 'd'
	case m&fs.ModeSymlink != 0:
		b[0] = 'l'
	case m&fs.ModeNamedPipe != 0:
		b[0] = 'p'
	case m&fs.ModeSocket != 0:
		b[0] = 's'
	case m&fs.ModeCharDevice != 0:
		b[0] = 'c'
	case m&fs.ModeDevice != 0:
		b[0] = 'b'
	}
	for i, c := range "rwxrwxrwx" {
		if m.Perm()&(1<<(8-i)) != 0 {
			b[i+1] = byte(c)
		}
	}
	special := func(i int, on bool, set, unset byte) {
		if !on {
			return
		}
		if b[i] == 'x' {
			b[i] = set
		} else {
			b[i] = unset
		}
	}
	special(3, m&fs.ModeSetuid != 0, 's', 'S')
	special(6, m&fs.ModeSetgid != 0, 's', 'S')
	special(9, m&fs.ModeSticky != 0, 't', 'T')
	return string(b)
}
