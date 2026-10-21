package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReopenInterval(t *testing.T) {
	for failures, want := range map[int]time.Duration{
		0: 30 * time.Second, 1: 30 * time.Second, 2: time.Minute,
		3: 2 * time.Minute, 4: 4 * time.Minute, 5: 5 * time.Minute, 50: 5 * time.Minute,
	} {
		if got := reopenInterval(failures); got != want {
			t.Errorf("reopenInterval(%d) = %v, want %v", failures, got, want)
		}
	}
}

func TestSchedulerAnswerIsAppliedOnTheNextTick(t *testing.T) {
	w := newWatcher(&srv{ctx: context.Background()})
	w.info["h"] = helperInfo{jobID: "7", status: "pending"}
	w.schedInflight["h"] = struct{}{}
	if !w.requestSchedulerStatus("h", true) {
		t.Error("a query already running should be reported, not started again")
	}
	if w.requestSchedulerStatus("none", false) {
		t.Error("a helper with no job has nothing to query")
	}

	w.schedResults <- schedResult{id: "h", err: errors.New("scheduler unavailable")}
	w.applySchedResults()
	if _, running := w.schedInflight["h"]; running {
		t.Error("an applied answer should end the query")
	}
	if w.schedCheckedAt["h"].IsZero() {
		t.Error("a failed answer still counts as asked, so a tunnel redial is not held back")
	}
	if got := w.info["h"].status; got != "pending" {
		t.Errorf("status = %q, want pending: a failed answer changes nothing", got)
	}
}
