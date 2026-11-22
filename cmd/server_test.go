package cmd

import (
	"os"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestRotateServerTokenReplacesTheTokenFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	settingstest.Override(t, "server.require_token", "true")
	path := config.GetServerTokenPath()

	if err := rotateServerToken(); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no token file after rotating: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	if err := rotateServerToken(); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(path); string(second) == string(first) {
		t.Error("a second rotation kept the same token")
	}
}

func TestRotateServerTokenWritesNothingWithTheTokenOff(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	settingstest.Override(t, "server.require_token", "false")
	if err := rotateServerToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.GetServerTokenPath()); err == nil {
		t.Error("a token file was written with server.require_token off")
	}
}
