package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestProjectCreateThenInfo(t *testing.T) {
	dir := t.TempDir()
	s := &srv{}

	get := func() projectInfo {
		req := httptest.NewRequest(http.MethodGet, "/api/project?"+url.Values{"cwd": {dir}}.Encode(), nil)
		rec := httptest.NewRecorder()
		s.handleProject(rec, req)
		var info projectInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		return info
	}

	if info := get(); info.Root != "" {
		t.Fatalf("root before create = %q, want none", info.Root)
	}
	if info := get(); info.Required == nil || info.Optional == nil {
		t.Fatalf("required = %v, optional = %v, want empty lists, not null", info.Required, info.Optional)
	}

	body := strings.NewReader(`{"cwd":"` + dir + `"}`)
	rec := httptest.NewRecorder()
	s.handleProject(rec, httptest.NewRequest(http.MethodPost, "/api/project", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body)
	}

	if info := get(); info.Root != dir || info.Root != info.CWD {
		t.Fatalf("root after create = %q, cwd = %q", info.Root, info.CWD)
	}
}

// Inside a project, a required overlay the project has not pinned resolves by
// installed name, as the launch does, rather than showing as unresolved.
func TestResolveRequiredInAProjectUsesInstalled(t *testing.T) {
	imagesDir := t.TempDir()
	installed := "ubuntu24--rstudio-server--2026.09.0-174.sqf"
	if err := os.WriteFile(filepath.Join(imagesDir, installed), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	prevPaths := config.GlobalDataPaths
	config.GlobalDataPaths.ImagesDirs = []string{imagesDir}
	settingstest.Override(t, "default_distro", "ubuntu24")
	t.Cleanup(func() { config.GlobalDataPaths = prevPaths })

	root := t.TempDir()
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	standing, err := project.StandingAt(root)
	if err != nil || standing == nil {
		t.Fatalf("StandingAt: standing = %v, err = %v", standing, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/project", nil)

	got := resolveRequired(req, standing, "ubuntu24", "rstudio-server")
	if got.Resolved != "ubuntu24/rstudio-server/2026.09.0-174" || got.Ident != "" {
		t.Errorf("an unpinned installed overlay resolved to %+v", got)
	}
	if got := resolveRequired(req, standing, "ubuntu24", "not-installed"); got.Resolved != "" {
		t.Errorf("an overlay nothing answers resolved to %+v", got)
	}
}

// An optional name resolves like a required one, and a name nothing answers comes back unresolved.
func TestProjectInfoResolvesOptionalNames(t *testing.T) {
	imagesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(imagesDir, "opencode--1.19.6.sqf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	prevPaths := config.GlobalDataPaths
	config.GlobalDataPaths.ImagesDirs = []string{imagesDir}
	settingstest.Override(t, "default_distro", "ubuntu24")
	t.Cleanup(func() { config.GlobalDataPaths = prevPaths })

	q := url.Values{"cwd": {t.TempDir()}, "optional": {"opencode", "codex"}}
	rec := httptest.NewRecorder()
	(&srv{}).handleProject(rec, httptest.NewRequest(http.MethodGet, "/api/project?"+q.Encode(), nil))
	var info projectInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Optional) != 2 || info.Optional[0].Resolved != "opencode/1.19.6" || info.Optional[1].Resolved != "" {
		t.Errorf("optional = %+v, want opencode resolved and codex not", info.Optional)
	}
	if len(info.Required) != 0 {
		t.Errorf("required = %+v, want none", info.Required)
	}
}
