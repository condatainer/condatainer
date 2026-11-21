package helper

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

// addHistoryRun records a run with a state folder, started n seconds after the epoch.
func addHistoryRun(t *testing.T, id, status string, n int) {
	t.Helper()
	if err := os.MkdirAll(config.GetHelperStateDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &HelperRun{ID: id, Name: "svc", Status: status, StartedAt: time.Unix(int64(n), 0)}
	if err := AppendHistory(run); err != nil {
		t.Fatal(err)
	}
}

// kept reports whether a run's history entry and state folder both exist.
func kept(t *testing.T, id string) bool {
	t.Helper()
	_, err := os.Stat(config.GetHelperStateDir(id))
	entry := HistoryEntryForID(id) != nil
	if (err == nil) != entry {
		t.Errorf("%s: state folder exists = %v, history entry exists = %v", id, err == nil, entry)
	}
	return entry
}

func TestPruneHistoryRemovesTheStateOfTheEntriesItDrops(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	addHistoryRun(t, "active", "running", 0)
	for i := 1; i <= 4; i++ {
		addHistoryRun(t, "done"+strconv.Itoa(i), "done", i)
	}

	if err := pruneHistory(2); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"active": true, "done1": false, "done2": false, "done3": true, "done4": true} {
		if got := kept(t, id); got != want {
			t.Errorf("%s kept = %v, want %v", id, got, want)
		}
	}
}

// Finishing a run prunes history to helper.history_limit finished entries. A run still going is never counted or dropped.
func TestFinishingARunPrunesToTheHistoryLimit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	settingstest.Override(t, "helper.history_limit", "1")
	addHistoryRun(t, "old1", "done", 1)
	addHistoryRun(t, "old2", "done", 2)
	for i, id := range []string{"run1", "run2", "run3"} {
		addHistoryRun(t, id, "running", 3+i)
	}

	if err := UpdateHistoryStatus("run1", "done", time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"old1": false, "old2": false, "run1": true, "run2": true, "run3": true} {
		if got := kept(t, id); got != want {
			t.Errorf("%s kept = %v, want %v", id, got, want)
		}
	}
}

// A limit of 0 keeps every finished entry.
func TestHistoryLimitZeroKeepsEverything(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	settingstest.Override(t, "helper.history_limit", "0")
	for i := 1; i <= 5; i++ {
		addHistoryRun(t, "done"+strconv.Itoa(i), "done", i)
	}
	addHistoryRun(t, "run", "running", 6)

	if err := UpdateHistoryStatus("run", "done", time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"done1", "done2", "done3", "done4", "done5", "run"} {
		if !kept(t, id) {
			t.Errorf("%s was pruned with a limit of 0", id)
		}
	}
}

func TestHistoryLimitRefusesANegativeValue(t *testing.T) {
	if restore, err := settings.OverrideValue("helper.history_limit", "-1"); err == nil {
		restore()
		t.Error("a negative helper.history_limit was accepted")
	}
}
