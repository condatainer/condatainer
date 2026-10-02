package utils

import (
	"errors"
	"testing"
)

// A command for another shell names the running binary, quoted when its path
// needs it, and falls back to the bare name.
func TestSelfCommand(t *testing.T) {
	prev := executable
	t.Cleanup(func() { executable = prev })

	for path, want := range map[string]string{
		"/apps/condatainer/0.2.0/bin/condatainer": "/apps/condatainer/0.2.0/bin/condatainer",
		"/apps/my tools/condatainer":              "'/apps/my tools/condatainer'",
		"/apps/it's/condatainer":                  `'/apps/it'\''s/condatainer'`,
	} {
		executable = func() (string, error) { return path, nil }
		if got := SelfCommand(); got != want {
			t.Errorf("SelfCommand() for %q = %q, want %q", path, got, want)
		}
	}
	executable = func() (string, error) { return "", errors.New("unreadable") }
	if got := SelfCommand(); got != "condatainer" {
		t.Errorf("SelfCommand() with no path = %q, want the bare name", got)
	}
}
