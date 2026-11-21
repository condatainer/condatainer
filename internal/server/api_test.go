package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/condatainer/condatainer/internal/helper"
)

// A helper link in the dashboard uses the port the browser reached the server on, which a forward can make differ from the listening port.
func TestRequestPortFollowsTheHostHeader(t *testing.T) {
	s := &srv{port: 13182}
	for _, tc := range []struct {
		host string
		want int
	}{
		{"localhost:13182", 13182},
		{"127.0.0.1:40000", 40000},
		{"code-server.localhost:40001", 40001},
		{"localhost", 80},
		{"", 13182},
		{"localhost:abc", 13182},
		{"localhost:0", 13182},
		{"localhost:70000", 13182},
		{"[::1]", 13182},
		{"[::1]:40002", 40002},
	} {
		r := httptest.NewRequest("GET", "/api/helpers", nil)
		r.Host = tc.host
		if got := s.requestPort(r); got != tc.want {
			t.Errorf("Host %q: port = %d, want %d", tc.host, got, tc.want)
		}
	}
}

// The helper list carries a link on the port the request used, so a forward on another local port still opens the helper.
func TestHelperListLinkUsesTheRequestPort(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	invalidateHistoryCache()
	t.Cleanup(invalidateHistoryCache)
	run := &helper.HelperRun{ID: "svc-20260101-000000", Name: "svc", Port: 9000, Status: "running"}
	if err := helper.AppendHistory(run); err != nil {
		t.Fatal(err)
	}
	s := &srv{ctx: context.Background(), port: 13182}
	s.watcher = newWatcher(s)

	for _, tc := range []struct{ host, want string }{
		{"localhost:13182", "http://svc-20260101-000000.localhost:13182/"},
		{"localhost:40000", "http://svc-20260101-000000.localhost:40000/"},
	} {
		r := httptest.NewRequest("GET", "/api/helpers", nil)
		r.Host = tc.host
		w := httptest.NewRecorder()
		s.handleHelpersList(w, r)
		var got []helperListEntry
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("Host %q: %v: %s", tc.host, err, w.Body.String())
		}
		if len(got) != 1 || got[0].AccessURL != tc.want {
			t.Errorf("Host %q: access_url = %+v, want %q", tc.host, got, tc.want)
		}
	}
}
