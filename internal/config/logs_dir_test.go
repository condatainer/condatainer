package config

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
