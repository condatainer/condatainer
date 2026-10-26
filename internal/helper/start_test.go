package helper

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
