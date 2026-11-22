package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/server"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

// captureFile returns what fn writes to *f, which is os.Stdout or os.Stderr.
func captureFile(t *testing.T, f **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := *f
	*f = w
	defer func() { *f = old }()
	fn()
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestServerStatusLinkCarriesTheTokenAlone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	settingstest.Override(t, "server.require_token", "true")
	tok, err := server.RotateToken()
	if err != nil {
		t.Fatal(err)
	}
	got := captureFile(t, &os.Stderr, func() { printServerLink(4321) })
	if want := "http://localhost:4321/?cnt_token=" + tok + "\n"; got != want {
		t.Errorf("link line = %q, want %q (the link alone on its line)", got, want)
	}

	settingstest.Override(t, "server.require_token", "false")
	got = captureFile(t, &os.Stderr, func() { printServerLink(4321) })
	if got != "http://localhost:4321/\n" {
		t.Errorf("with the token off: %q, want the plain link", got)
	}
	if strings.Contains(got, "cnt_token") {
		t.Error("the plain link carries a token")
	}
}
