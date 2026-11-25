package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
)

// TestFSListingPermissions checks that a listing reports each entry's mode
// in ls form, including the setuid, setgid and sticky bits, plus owner and group.
func TestFSListingPermissions(t *testing.T) {
	root := t.TempDir()
	want := map[string]string{
		"private": "drwxr-x---",
		"shared":  "drwxr-sr-x",
		"tmp":     "drwxrwxrwt",
		"secret":  "-rw-------",
		"tool":    "-rwsr-xr-x",
		"noexec":  "-rwSr--r--",
	}
	modes := map[string]os.FileMode{
		"private": 0o750,
		"shared":  0o755 | os.ModeSetgid,
		"tmp":     0o777 | os.ModeSticky,
		"secret":  0o600,
		"tool":    0o755 | os.ModeSetuid,
		"noexec":  0o644 | os.ModeSetuid,
	}
	for name, mode := range modes {
		path := filepath.Join(root, name)
		var err error
		if want[name][0] == 'd' {
			err = os.Mkdir(path, 0o700)
		} else {
			err = os.WriteFile(path, []byte("x"), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/api/fs?path="+url.QueryEscape(root), nil)
	w := httptest.NewRecorder()
	(&srv{}).handleFS(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var got []struct{ Name, Mode, Owner, Group string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	me, err := user.Current()
	if err != nil {
		t.Skip("no current user:", err)
	}
	for _, e := range got {
		if e.Mode != want[e.Name] {
			t.Errorf("%s: mode = %q, want %q", e.Name, e.Mode, want[e.Name])
		}
		if e.Owner != me.Username {
			t.Errorf("%s: owner = %q, want %q", e.Name, e.Owner, me.Username)
		}
		if e.Group == "" {
			t.Errorf("%s: group is empty", e.Name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d entries, want %d", len(got), len(want))
	}
}

// TestGetentName checks that a name comes from getent, and that an id with no
// name is an error.
func TestGetentName(t *testing.T) {
	if _, err := exec.LookPath("getent"); err != nil {
		t.Skip("getent not found")
	}
	if name, err := getentName("passwd", "0"); err != nil || name != "root" {
		t.Errorf("getentName(passwd, 0) = %q, %v, want root", name, err)
	}
	if name, err := getentName("group", "4294967000"); err == nil {
		t.Errorf("getentName(group, unused id) = %q, want an error", name)
	}
}
