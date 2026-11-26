package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
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

// The helper list carries a walltime as seconds only, and the page writes its own text.
func TestHelperListHasWalltimeAsSecondsOnly(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	invalidateHistoryCache()
	t.Cleanup(invalidateHistoryCache)
	run := &helper.HelperRun{ID: "svc-20260101-000000", Name: "svc", Status: "running", WalltimeSecs: 7200}
	if err := helper.AppendHistory(run); err != nil {
		t.Fatal(err)
	}
	s := &srv{ctx: context.Background(), port: 13182}
	s.watcher = newWatcher(s)
	w := httptest.NewRecorder()
	s.handleHelpersList(w, httptest.NewRequest("GET", "/api/helpers", nil))
	body := w.Body.String()
	if !strings.Contains(body, `"walltime_secs":7200`) || strings.Contains(body, "walltime_str") {
		t.Errorf("helper list = %s, want walltime_secs and no walltime_str", body)
	}
}

// listHelpers calls the helper list with a request target and decodes the reply.
func listHelpers(t *testing.T, s *srv, target string) ([]helperListEntry, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleHelpersList(w, httptest.NewRequest("GET", target, nil))
	var got []helperListEntry
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: %v: %s", target, err, w.Body.String())
		}
	}
	return got, w
}

func finishedIDs(list []helperListEntry) []string {
	var ids []string
	for _, e := range list {
		if !isActiveRunStatus(e.Status) {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// The helper list holds every active run and one page of finished runs, the last ended first. A cursor continues after the last run a caller has.
func TestHelperListPagesFinishedRuns(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	invalidateHistoryCache()
	t.Cleanup(invalidateHistoryCache)
	base := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)
	add := func(id, status string, ended time.Time) {
		run := &helper.HelperRun{ID: id, Name: "svc", Port: 9000, Status: status}
		if status == "done" {
			run.EndedAt = &ended
		}
		if err := helper.AppendHistory(run); err != nil {
			t.Fatal(err)
		}
	}
	add("long-running", "running", time.Time{})
	for i := 0; i < 120; i++ {
		add(fmt.Sprintf("done-%03d", i), "done", base.Add(time.Duration(i)*time.Second))
	}
	tie := base.Add(500 * time.Second)
	add("tie-a", "done", tie)
	add("tie-b", "done", tie)
	s := &srv{ctx: context.Background(), port: 13182}
	s.watcher = newWatcher(s)

	first, w := listHelpers(t, s, "/api/helpers")
	if len(first) != 51 || first[0].ID != "long-running" {
		t.Fatalf("first page = %d entries starting %q, want the active run and 50 finished", len(first), first[0].ID)
	}
	if got := w.Header().Get("X-Total-Count"); got != "122" {
		t.Errorf("X-Total-Count = %q, want 122", got)
	}
	if ids := finishedIDs(first); ids[0] != "tie-b" || ids[1] != "tie-a" || ids[2] != "done-119" {
		t.Errorf("finished runs start %v, want the last ended first and the tie by id", ids[:3])
	}

	seen := map[string]bool{}
	var pages []int
	list := first
	for {
		ids := finishedIDs(list)
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("%s listed twice", id)
			}
			seen[id] = true
		}
		pages = append(pages, len(ids))
		last := list[len(list)-1]
		if len(ids) < 50 {
			break
		}
		target := fmt.Sprintf("/api/helpers?before=%s&before_id=%s", url.QueryEscape(last.EndedAt.Format(time.RFC3339Nano)), last.ID)
		list, _ = listHelpers(t, s, target)
		for _, e := range list {
			if e.ID == "long-running" {
				t.Fatal("a page after the first repeats an active run")
			}
		}
	}
	if len(seen) != 122 || !slices.Equal(pages, []int{50, 50, 22}) {
		t.Errorf("pages = %v, finished seen = %d, want 50, 50, 22 and 122", pages, len(seen))
	}

	if got, _ := listHelpers(t, s, "/api/helpers?limit=10"); len(got) != 11 {
		t.Errorf("limit=10: %d entries, want the active run and 10", len(got))
	}
	if got, w := listHelpers(t, s, "/api/helpers?status=active"); len(got) != 1 || w.Header().Get("X-Total-Count") != "0" {
		t.Errorf("status=active: %d entries, total %q, want only the active run and 0", len(got), w.Header().Get("X-Total-Count"))
	}

	// A cursor needs no entry to exist.
	missing := fmt.Sprintf("/api/helpers?limit=1&before=%s&before_id=zzz-pruned", url.QueryEscape(base.Add(100*time.Second).Format(time.RFC3339Nano)))
	if got, _ := listHelpers(t, s, missing); len(got) != 1 || got[0].ID != "done-100" {
		t.Errorf("cursor on a pruned run gave %v, want done-100", finishedIDs(got))
	}
	// A tie continues after the run the caller has.
	afterTie := fmt.Sprintf("/api/helpers?limit=1&before=%s&before_id=tie-b", url.QueryEscape(tie.Format(time.RFC3339Nano)))
	if got, _ := listHelpers(t, s, afterTie); len(got) != 1 || got[0].ID != "tie-a" {
		t.Errorf("after tie-b gave %v, want tie-a", finishedIDs(got))
	}
	if _, w := listHelpers(t, s, "/api/helpers?before=yesterday"); w.Code != 400 {
		t.Errorf("a bad before gave %d, want 400", w.Code)
	}
}

// The dashboard's own replies never hold the token. A link in the list is the plain one.
func TestHelperListNeverCarriesTheToken(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	invalidateHistoryCache()
	t.Cleanup(invalidateHistoryCache)
	settingstest.Override(t, "server.require_token", "true")
	tok, err := createTokenFile(config.GetServerTokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.AppendHistory(&helper.HelperRun{ID: "svc-20260101-000000", Name: "svc", Port: 9000, Status: "running", URLPath: "?token=jup"}); err != nil {
		t.Fatal(err)
	}
	s := &srv{ctx: context.Background(), port: 13182}
	s.watcher = newWatcher(s)
	r := httptest.NewRequest("GET", "/api/helpers", nil)
	r.Host = "localhost:13182"
	w := httptest.NewRecorder()
	s.handleHelpersList(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "svc-20260101-000000.localhost:13182/?token=jup") {
		t.Fatalf("the list has no access_url: %s", body)
	}
	if strings.Contains(body, tok) || strings.Contains(body, tokenParam) {
		t.Errorf("the list carries the token: %s", body)
	}
}

func TestGpuOptionsOf(t *testing.T) {
	info := &scheduler.ClusterInfo{
		Limits: []scheduler.ResourceLimits{{Partition: "compute", Default: true}, {Partition: "gpu"}, {Partition: "gpu-long"}},
		AvailableGpus: []scheduler.GpuInfo{
			{Type: "h100", Partition: "gpu"}, {Type: "a100", Partition: "gpu-long"},
			{Type: "a100", Partition: "gpu"}, {Type: "gpu", Partition: "gpu"}, {Type: "a100", Partition: "gpu"},
		},
	}
	got := gpuOptionsOf(info, scheduler.SchedulerSLURM)
	want := gpuOptions{DefaultPartition: "compute", GPUs: []gpuOption{
		{"a100", "gpu"}, {"a100", "gpu-long"}, {"h100", "gpu"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Slurm with a default partition = %+v, want %+v", got, want)
	}

	plain := gpuOptions{GPUs: []gpuOption{{Type: "a100"}, {Type: "h100"}}}
	if got := gpuOptionsOf(info, scheduler.SchedulerLSF); !reflect.DeepEqual(got, plain) {
		t.Errorf("LSF = %+v, want plain types", got)
	}
	info.Limits[0].Default = false
	if got := gpuOptionsOf(info, scheduler.SchedulerSLURM); !reflect.DeepEqual(got, plain) {
		t.Errorf("Slurm with no default partition = %+v, want plain types", got)
	}
	if got := gpuOptionsOf(&scheduler.ClusterInfo{}, scheduler.SchedulerSLURM); got.GPUs == nil {
		t.Error("no GPUs must be an empty list, not null")
	}
}

// A saved mem or time is stored with its unit, comes back as a number too, and one that does not parse is refused.
func TestHelperConfigStoresUnitsAndGivesNumbers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := &srv{}
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleHelperConfig(rec, httptest.NewRequest("POST", "/api/helpers/igv/config", strings.NewReader(body)), "igv")
		return rec
	}
	rec := post(`{"mem":"16","time":"1:30","partition":"gpu"}`)
	var got helperConfigReply
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	if got.Values["mem"] != "16G" || got.Values["time"] != "1h30m" || got.Values["partition"] != "gpu" ||
		got.MemMB != 16*1024 || got.TimeSecs != 5400 {
		t.Errorf("reply = %+v", got)
	}
	if rec := post(`{"time":"soon"}`); rec.Code != 400 {
		t.Errorf("a time that does not parse gave %d, want 400", rec.Code)
	}
}
