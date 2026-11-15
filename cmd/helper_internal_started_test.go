package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/helper"
)

func TestServerStartedRecordsTheNodeAndJob(t *testing.T) {
	t.Setenv("CNT_HELPER_ID", "t-1")
	t.Setenv("CNT_HELPER_STATE_DIR", t.TempDir())
	t.Setenv("CNT_HELPER_JOB_ID", "42")
	serverStartedNode = "n1.example"
	t.Cleanup(func() { serverStartedNode = "" })
	if err := serverStartedCmd.RunE(serverStartedCmd, nil); err != nil {
		t.Fatal(err)
	}
	events, _, err := helper.ReadEventsFrom("t-1", 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, err = %v", events, err)
	}
	if ev := events[0]; ev.Type != "start" || ev.Node != "n1.example" || ev.JobID != "42" {
		t.Errorf("event = %+v, want a start on n1.example for job 42", ev)
	}
}
