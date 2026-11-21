package server

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestValidateToken(t *testing.T) {
	good := strings.Repeat("a", 16)
	if got, err := validateToken(good + "\n"); err != nil || got != good {
		t.Errorf("a token with a trailing newline = %q, %v; want %q", got, err, good)
	}
	if _, err := validateToken("A-b_c.d~e0123456789"); err != nil {
		t.Errorf("a token of the allowed characters was refused: %v", err)
	}
	for name, bad := range map[string]string{
		"short":       strings.Repeat("a", 15),
		"space":       strings.Repeat("a", 16) + " b",
		"slash":       strings.Repeat("a", 16) + "/",
		"non-ascii":   strings.Repeat("a", 16) + "é",
		"empty":       "",
		"inner break": strings.Repeat("a", 16) + "\nb",
	} {
		if _, err := validateToken(bad); !errors.Is(err, ErrTokenInvalid) {
			t.Errorf("%s: err = %v, want ErrTokenInvalid", name, err)
		}
	}
}

func TestCreateTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "server.token")
	tok, err := createTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 16 {
		t.Errorf("token has %d characters, want 16 (12 bytes in base64url)", len(tok))
	}
	if _, err := validateToken(tok); err != nil {
		t.Errorf("a generated token fails the rules for an edited one: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if got, err := readTokenFile(path); err != nil || got != tok {
		t.Errorf("read back %q, %v; want %q", got, err, tok)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the state directory holds %d files, want only the token (no temporary file)", len(entries))
	}
}

func TestConcurrentCreationsShareOneToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.token")
	const n = 8
	got := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = createTokenFile(path)
		}()
	}
	wg.Wait()
	for i, tok := range got {
		if tok == "" || tok != got[0] {
			t.Errorf("creation %d returned %q, want every creation to return %q", i, tok, got[0])
		}
	}
	if onDisk, _ := readTokenFile(path); onDisk != got[0] {
		t.Errorf("file holds %q, want %q", onDisk, got[0])
	}
}

func TestTokenFileRefusedWhenOthersCanRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := readTokenFile(path); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a group-readable file: err = %v, want ErrTokenInvalid", err)
	}
}

// A store reads the file when a request does not match, so an edited token works at once,
// and it keeps the token it has when the file is missing or refused.
func TestTokenStoreFollowsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.token")
	store := newTokenStore(path, quietLog())
	if err := store.ensure(); err != nil {
		t.Fatal(err)
	}
	old, _ := readTokenFile(path)
	if !store.check(old) || store.check("") || store.check("nope") {
		t.Fatal("the store did not accept its token, or accepted a wrong one")
	}

	edit := func(content string, mode os.FileMode) {
		t.Helper()
		tmp := path + ".new"
		if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	fresh := "memorable-token-0123456789"
	edit(fresh+"\n", 0o600)
	store.mu.Lock()
	store.missAt = time.Time{} // a mismatch within the last second does not force another read
	store.mu.Unlock()
	if !store.check(fresh) {
		t.Error("a request with the edited token was refused, want it accepted at once")
	}
	// The old token still matches only until the store looks at the file again.
	store.mu.Lock()
	store.loadedAt = time.Now().Add(-tokenRecheck - time.Second)
	store.mu.Unlock()
	if store.check(old) {
		t.Error("the old token was accepted after the file changed and the store looked again")
	}

	edit("short\n", 0o600) // refused: the store keeps its token
	store.mu.Lock()
	store.loadedAt, store.missAt = time.Time{}, time.Time{}
	store.mu.Unlock()
	if !store.check(fresh) {
		t.Error("a refused value replaced the server's token")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.loadedAt, store.missAt = time.Time{}, time.Time{}
	store.mu.Unlock()
	if !store.check(fresh) {
		t.Error("a deleted file replaced the server's token")
	}
}

// A refused file stops a starting server instead of leaving it open.
func TestTokenStoreEnsureReportsARefusedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.token")
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := newTokenStore(path, quietLog()).ensure(); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("ensure = %v, want ErrTokenInvalid", err)
	}
}
