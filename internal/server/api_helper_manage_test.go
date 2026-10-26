package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
)

func TestHelperManagerListsOfferedAndInstalled(t *testing.T) {
	src := t.TempDir()
	for name, body := range map[string]string{"svc": "#!/bin/bash\n#DESC: a service\n", "other": "#!/bin/bash\n"} {
		p := filepath.Join(src, "helpers", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setTestSource(t, src)

	dir := t.TempDir()
	old := config.GlobalDataPaths.HelperScriptsDirs
	config.GlobalDataPaths.HelperScriptsDirs = []string{dir}
	t.Cleanup(func() { config.GlobalDataPaths.HelperScriptsDirs = old })
	if err := os.WriteFile(filepath.Join(dir, "svc"), []byte("#!/bin/bash\n#DESC: a service\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := helper.UpdateInstalled(dir, func(r *helper.Installed) error {
		r.Helpers["svc"] = helper.InstalledEntry{Source: "test", SHA256: "stale"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	(&srv{}).handleHelperManager(w, httptest.NewRequest(http.MethodGet, "/api/helpers/manager", nil))
	var got managerResp
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status %d, %v", w.Code, err)
	}
	states := map[string]string{}
	for _, o := range got.Offered {
		states[o.Name] = o.State
	}
	// The recorded hash is not the source's, but the file differs from it too, so the helper is edited, not newer.
	if len(got.Offered) != 2 || states["svc"] != "added" || states["other"] != "" {
		t.Errorf("offered = %+v", got.Offered)
	}
	if len(got.Installed) != 1 || len(got.Installed[0].Scripts) != 1 || !got.Installed[0].Scripts[0].Added {
		t.Errorf("installed = %+v", got.Installed)
	}
}

func TestHelperChangeRequestsAreChecked(t *testing.T) {
	post := func(h http.HandlerFunc, body string) int {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		return w.Code
	}
	s := &srv{}
	if code := post(s.handleHelperAdd, `{"names":[]}`); code != http.StatusBadRequest {
		t.Errorf("add without names: %d", code)
	}
	if code := post(s.handleHelperRemove, `{"names":["a"],"layer":"nowhere"}`); code != http.StatusBadRequest {
		t.Errorf("remove in an unknown layer: %d", code)
	}
	w := httptest.NewRecorder()
	s.handleHelperAdd(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET add: %d", w.Code)
	}
}
