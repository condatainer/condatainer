package helper

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/config"
)

func TestPruneHistoryRemovesTheStateOfTheEntriesItDrops(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	add := func(id, status string, n int) {
		t.Helper()
		if err := os.MkdirAll(config.GetHelperStateDir(id), 0o755); err != nil {
			t.Fatal(err)
		}
		run := &HelperRun{ID: id, Name: "svc", Status: status, StartedAt: time.Unix(int64(n), 0)}
		if err := AppendHistory(run); err != nil {
			t.Fatal(err)
		}
	}
	add("active", "running", 0)
	for i := 1; i <= 4; i++ {
		add("done"+strconv.Itoa(i), "done", i)
	}

	if err := pruneHistory(2); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"active": true, "done1": false, "done2": false, "done3": true, "done4": true} {
		_, err := os.Stat(config.GetHelperStateDir(id))
		if got := err == nil; got != want {
			t.Errorf("state of %s exists = %v, want %v", id, got, want)
		}
		if got := HistoryEntryForID(id) != nil; got != want {
			t.Errorf("history entry of %s exists = %v, want %v", id, got, want)
		}
	}
}
