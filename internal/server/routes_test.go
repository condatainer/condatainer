package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDashboardFilesAreNotCached checks that the page and its scripts ask the
// browser to refetch them on every load.
func TestDashboardFilesAreNotCached(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux, &srv{})
	for _, path := range []string{"/", "/static/app.js", "/static/style.css"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, w.Code)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want no-cache", path, got)
		}
	}
}

// TestStateChangingRoutesRefuseGET checks that a GET changes nothing. A cross-site page can make
// the browser send a GET with the sign-in cookie, so every route that changes state refuses it.
func TestStateChangingRoutesRefuseGET(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux, &srv{})
	for _, path := range []string{
		"/api/helpers/add", "/api/helpers/remove", "/api/helpers/upgrade", "/api/helpers/finished",
		"/api/helpers/x/stop", "/api/helpers/x/delete", "/api/helpers/x/start",
		"/api/overlay/create", "/api/overlay/edit", "/api/create", "/api/remove",
		"/api/fs/upload", "/api/fs/upload/check", "/api/fs/copy", "/api/tasks/x/cancel",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status %d, want 405", path, w.Code)
		}
	}
}
