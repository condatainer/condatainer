package helper

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/config"
)

func TestPollOnceStart(t *testing.T) {
	t.Setenv("CNT_HELPER_ID", "demo-1")
	t.Setenv("CNT_HELPER_STATE_DIR", t.TempDir())
	if err := AppendEvent("demo-1", StateEvent{Type: "start", Node: "node07.cluster.edu", JobID: "42"}); err != nil {
		t.Fatal(err)
	}

	got := PollOnce("demo-1", 0)
	if got.Start == nil || got.Start.Node != "node07.cluster.edu" || got.Start.JobID != "42" {
		t.Fatalf("Start = %+v; want node07.cluster.edu, job 42", got.Start)
	}
	if got.Ready != nil || got.Done != nil {
		t.Errorf("Ready = %+v, Done = %+v; want neither", got.Ready, got.Done)
	}
	if st, _ := ReadStartEvent("demo-1"); st == nil || st.Node != "node07.cluster.edu" {
		t.Errorf("ReadStartEvent = %+v; want node07.cluster.edu", st)
	}
}

// A start moves a pending run to starting and records the node. A later ready keeps that node.
func TestStartThenReadyKeepsNode(t *testing.T) {
	run := HelperRun{Status: "pending"}
	run.ApplyStart(&StartState{Node: "node07.cluster.edu"})
	if run.Status != "starting" || run.Node != "node07.cluster.edu" {
		t.Fatalf("after start: status %q, node %q", run.Status, run.Node)
	}

	run.ApplyReady(&ReadyState{Node: "node07", Port: 8080, WalltimeSec: 3600, Timestamp: time.Unix(100, 0)})
	if run.Status != "running" || run.Node != "node07.cluster.edu" {
		t.Errorf("after ready: status %q, node %q; want running, node07.cluster.edu", run.Status, run.Node)
	}
	if run.Port != 8080 || run.Walltime() != time.Hour {
		t.Errorf("port %d, walltime %v; want 8080, 1h", run.Port, run.Walltime())
	}
}

func TestReadyWithoutStartSetsNode(t *testing.T) {
	run := HelperRun{Status: "pending"}
	run.ApplyReady(&ReadyState{Node: "node07"})
	if run.Node != "node07" {
		t.Errorf("node %q; want node07", run.Node)
	}
}

// A start that arrives after the run moved on does not reopen its status.
func TestApplyStartKeepsLaterStatus(t *testing.T) {
	run := HelperRun{Status: "running", Node: "a.b"}
	run.ApplyStart(&StartState{Node: "a.b"})
	if run.Status != "running" {
		t.Errorf("status %q; want running", run.Status)
	}
}

func TestShortNode(t *testing.T) {
	for in, want := range map[string]string{
		"node07.cluster.edu": "node07",
		"node07":             "node07",
		"10.0.0.5":           "10.0.0.5",
		"":                   "",
	} {
		if got := ShortNode(in); got != want {
			t.Errorf("ShortNode(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestHistoryRecordStoresWalltimeInSeconds(t *testing.T) {
	data, err := json.Marshal(HelperRun{ID: "x", WalltimeSecs: 43200})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"walltime_secs":43200`) {
		t.Errorf("record = %s; want walltime_secs in seconds", data)
	}
	if got := (&HelperRun{WalltimeSecs: 43200}).Walltime(); got != 12*time.Hour {
		t.Errorf("Walltime() = %v, want 12h", got)
	}
}

// A usage event is read back, is ignored by the poll, and is copied into the run's record when it ends.
func TestUsageEventReachesTheHistoryRecord(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	addHistoryRun(t, "svc-1", "running", 1)
	t.Setenv("CNT_HELPER_ID", "svc-1")
	t.Setenv("CNT_HELPER_STATE_DIR", config.GetHelperStateDir("svc-1"))

	used := &UsageStats{WallSecs: 3441, CPUSecs: 15709, PeakMemMB: 31950}
	if err := AppendEvent("svc-1", StateEvent{Type: "usage", Usage: used}); err != nil {
		t.Fatal(err)
	}
	if got := PollOnce("svc-1", 0); got.Start != nil || got.Ready != nil || got.Done != nil || len(got.NewMessages) != 0 {
		t.Errorf("a usage event changed the poll: %+v", got)
	}
	if got, _ := ReadUsageEvent("svc-1"); got == nil || *got != *used {
		t.Fatalf("ReadUsageEvent = %+v, want %+v", got, used)
	}

	if err := UpdateHistoryStatus("svc-1", "done", time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	run := HistoryEntryForID("svc-1")
	if run == nil || run.Usage == nil || *run.Usage != *used {
		t.Fatalf("history usage = %+v, want %+v", run, used)
	}
	data, _ := json.Marshal(run)
	if !strings.Contains(string(data), `"usage":{"wall_secs":3441,"cpu_secs":15709,"peak_mem_mb":31950}`) ||
		!strings.Contains(string(data), `"walltime_secs":0`) {
		t.Errorf("record = %s", data)
	}
}

// A run that wrote no usage event has no usage in its record.
func TestRunWithoutUsageEventHasNone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	addHistoryRun(t, "svc-2", "running", 1)
	if err := UpdateHistoryStatus("svc-2", "done", time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	if run := HistoryEntryForID("svc-2"); run == nil || run.Usage != nil {
		t.Errorf("history entry = %+v, want no usage", run)
	}
	data, _ := json.Marshal(HelperRun{ID: "x"})
	if strings.Contains(string(data), "usage") {
		t.Errorf("record = %s; a run that never measured carries no usage", data)
	}
}
