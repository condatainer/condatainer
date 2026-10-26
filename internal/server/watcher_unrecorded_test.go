package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
)

// A state directory with no history entry is looked up once a minute, not every tick.
func TestStateDirectoryWithoutAnEntryIsNotReadEveryTick(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(config.GetHelperStateDir("late"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(&srv{ctx: context.Background()})

	w.pollHelper("late")
	if _, seen := w.unrecorded["late"]; !seen || len(w.info) != 0 {
		t.Fatalf("an unrecorded directory should be remembered: unrecorded = %v, info = %v", w.unrecorded, w.info)
	}

	if err := helper.AppendHistory(&helper.HelperRun{ID: "late", Name: "svc", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	w.pollHelper("late")
	if _, known := w.info["late"]; known {
		t.Error("the entry was read again inside the recheck window")
	}

	w.unrecorded["late"] = time.Now().Add(-2 * unrecordedRecheck)
	w.pollHelper("late")
	if _, known := w.info["late"]; !known {
		t.Error("the entry was not picked up after the recheck window")
	}
	if _, seen := w.unrecorded["late"]; seen {
		t.Error("a recorded directory is still listed as unrecorded")
	}
}
