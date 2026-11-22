package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestTokenLink(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	settingstest.Override(t, "server.require_token", "true")
	path := config.GetServerTokenPath()

	// No token file yet: the link comes back as it is, and the call creates nothing.
	if got := TokenLink("http://localhost:1/"); got != "http://localhost:1/" {
		t.Errorf("with no token file: %q", got)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("TokenLink created the token file")
	}

	tok, err := createTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"http://localhost:1/":                   "http://localhost:1/?cnt_token=" + tok,
		"http://code.localhost:1/":              "http://code.localhost:1/?cnt_token=" + tok,
		"http://code.localhost:1/lab?token=jup": "http://code.localhost:1/lab?token=jup&cnt_token=" + tok,
		"https://vscode.dev/tunnel/x":           "https://vscode.dev/tunnel/x",
		"http://notlocalhost.example:1/":        "http://notlocalhost.example:1/",
		"":                                      "",
	} {
		if got := TokenLink(in); got != want {
			t.Errorf("TokenLink(%q) = %q, want %q", in, got, want)
		}
	}

	settingstest.Override(t, "server.require_token", "false")
	if got := TokenLink("http://localhost:1/"); got != "http://localhost:1/" {
		t.Errorf("with the token off: %q", got)
	}
}

// The dashboard runs the helper code in this process, and what it prints reaches the page and the API.
// So only the commands, under cmd/, may add the token to a link.
func TestOnlyCommandsAddTheTokenToALink(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if filepath.Base(path) == "tokenlink.go" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(data), "TokenLink(") {
			t.Errorf("%s calls TokenLink; only the commands under cmd/ may", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
