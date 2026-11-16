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
