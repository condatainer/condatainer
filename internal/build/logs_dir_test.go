package build

import "testing"

func TestDefaultLogsDir(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("SCRATCH", "")
	if got := DefaultLogsDir(); got != "/home/u/logs" {
		t.Errorf("no SCRATCH: got %q, want /home/u/logs", got)
	}
	t.Setenv("SCRATCH", "/scratch/u")
	if got := DefaultLogsDir(); got != "/scratch/u/logs" {
		t.Errorf("SCRATCH set: got %q, want /scratch/u/logs", got)
	}
}

// The default follows HOME and SCRATCH at the moment it is read, so a replaced HOME moves it.
func TestLogsDirDefaultFollowsHome(t *testing.T) {
	t.Setenv("SCRATCH", "")
	t.Setenv("HOME", "/home/a")
	if got := LogsDir(); got != "/home/a/logs" {
		t.Errorf("LogsDir = %q", got)
	}
	t.Setenv("HOME", "/home/b")
	if got := LogsDir(); got != "/home/b/logs" {
		t.Errorf("LogsDir after HOME changed = %q", got)
	}
}
