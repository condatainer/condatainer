package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/server"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

// helperListEnv sets up a running server on port 4321 and two running helpers: one served by the
// dashboard, and one with an external link. It returns the token.
func helperListEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	settingstest.Override(t, "server.require_token", "true")
	tok, err := server.RotateToken()
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := json.Marshal(config.ServerState{Host: "h", Port: 4321, PID: os.Getpid()})
	pidFile := config.GetServerPidFilePath()
	if err := os.MkdirAll(config.GetUserStateDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFile, pid, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, r := range []*helper.HelperRun{
		{ID: "svc-20260101-000000", Name: "svc", Runner: "SLURM", JobID: "1", Port: 9000, Status: "running", URLPath: "?token=jup", StartedAt: now, WalltimeSecs: 3600},
		{ID: "tunnel-20260101-000001", Name: "tunnel", Runner: "SLURM", JobID: "2", Status: "running", ExternalURL: "https://vscode.dev/tunnel/abc", StartedAt: now, WalltimeSecs: 3600},
	} {
		if err := helper.AppendHistory(r); err != nil {
			t.Fatal(err)
		}
	}
	return tok
}

func TestHelperListPrintsLinksWithTheToken(t *testing.T) {
	tok := helperListEnv(t)
	out := captureFile(t, &os.Stdout, func() {
		if err := showHelperStatus(""); err != nil {
			t.Fatal(err)
		}
	})
	// stdout here is a pipe, not a terminal: the token is printed all the same.
	if want := "http://svc-20260101-000000.localhost:4321/?token=jup&cnt_token=" + tok; !strings.Contains(out, "  "+want+"\n") {
		t.Errorf("no line with %q (on a line of its own):\n%s", want, out)
	}
	if !strings.Contains(out, "  https://vscode.dev/tunnel/abc\n") {
		t.Errorf("the external link was changed:\n%s", out)
	}
}

func TestHelperListPrintsPlainLinksWithTheTokenOff(t *testing.T) {
	helperListEnv(t)
	settingstest.Override(t, "server.require_token", "false")
	out := captureFile(t, &os.Stdout, func() {
		if err := showHelperStatus(""); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "cnt_token") {
		t.Errorf("a link carries a token with the token off:\n%s", out)
	}
	if !strings.Contains(out, "http://svc-20260101-000000.localhost:4321/?token=jup\n") {
		t.Errorf("no plain link:\n%s", out)
	}
}
