package server

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// never is a store's age limit that never replaces the token.
func never() time.Duration { return 0 }

// settle waits for the read in progress, if any, to end.
func settle(t *tokenStore) {
	for {
		t.mu.Lock()
		r := t.refreshing
		t.mu.Unlock()
		if r == nil {
			return
		}
		<-r
	}
}

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
	if got, _, err := readTokenFile(path); err != nil || got != tok {
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
	if onDisk, _, _ := readTokenFile(path); onDisk != got[0] {
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
	if _, _, err := readTokenFile(path); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a group-readable file: err = %v, want ErrTokenInvalid", err)
	}
}

// A store reads the file when a request does not match, so an edited token works at once,
// and it keeps the token it has when the file is missing or refused.
func TestTokenStoreFollowsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.token")
	store := newTokenStore(path, quietLog(), never)
	if err := store.ensure(); err != nil {
		t.Fatal(err)
	}
	old, _, _ := readTokenFile(path)
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
	store.check(fresh) // starts the read
	settle(store)
	if !store.check(fresh) {
		t.Error("a refused value replaced the server's token")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.loadedAt, store.missAt = time.Time{}, time.Time{}
	store.mu.Unlock()
	store.check(fresh) // starts the read
	settle(store)
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
	if err := newTokenStore(path, quietLog(), never).ensure(); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("ensure = %v, want ErrTokenInvalid", err)
	}
}

// ageStore returns a store on a token file last written age ago, replacing tokens older than limit.
func ageStore(t *testing.T, age, limit time.Duration) (*tokenStore, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.token")
	tok, err := createTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return newTokenStore(path, quietLog(), func() time.Duration { return limit }), path, tok
}

func TestStartingServerReplacesAnOldToken(t *testing.T) {
	day := 24 * time.Hour
	store, path, old := ageStore(t, 40*day, 30*day)
	if err := store.ensure(); err != nil {
		t.Fatal(err)
	}
	fresh, mod, err := readTokenFile(path)
	if err != nil || fresh == old || !store.check(fresh) || store.check(old) {
		t.Fatalf("the old token was not replaced: file %q, old %q, err %v", fresh, old, err)
	}
	if time.Since(mod) > time.Minute {
		t.Errorf("the replacement is %v old, want it new", time.Since(mod))
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRunningServerReplacesATokenThatGrewOld(t *testing.T) {
	day := 24 * time.Hour
	store, path, old := ageStore(t, day, 30*day)
	if err := store.ensure(); err != nil {
		t.Fatal(err)
	}
	if !store.check(old) {
		t.Fatal("a young token was refused")
	}
	when := time.Now().Add(-31 * day)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.loadedAt = time.Time{} // the next request looks at the file
	store.mu.Unlock()
	if store.check(old) {
		t.Error("the old token is still accepted after it passed server.token_days")
	}
	if fresh, _, _ := readTokenFile(path); fresh == old || !store.check(fresh) {
		t.Error("the replacement is not the token the file holds")
	}
}

func TestTokenDaysZeroNeverReplaces(t *testing.T) {
	store, _, old := ageStore(t, 3650*24*time.Hour, 0)
	if err := store.ensure(); err != nil {
		t.Fatal(err)
	}
	if !store.check(old) {
		t.Error("a ten-year-old token was replaced with server.token_days at 0")
	}
}

func TestTwoServersReplaceAnOldTokenOnce(t *testing.T) {
	day := 24 * time.Hour
	a, path, old := ageStore(t, 40*day, 30*day)
	b := newTokenStore(path, quietLog(), func() time.Duration { return 30 * day })
	var wg sync.WaitGroup
	for _, s := range []*tokenStore{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); s.ensure() }() //nolint:errcheck
	}
	wg.Wait()
	// Each server reads the file within a few seconds and ends with the token it holds.
	for _, s := range []*tokenStore{a, b} {
		s.mu.Lock()
		s.loadedAt, s.missAt = time.Time{}, time.Time{}
		s.mu.Unlock()
	}
	fresh, _, _ := readTokenFile(path)
	if fresh == old || !a.check(fresh) || !b.check(fresh) || a.check(old) || b.check(old) {
		t.Errorf("the two servers do not end with the one new token %q", fresh)
	}
}

// A node that finds the file already replaced takes that token and writes nothing.
func TestOldTokenAlreadyReplacedElsewhereIsTaken(t *testing.T) {
	day := 24 * time.Hour
	store, path, fresh := ageStore(t, time.Minute, 30*day)
	got := store.replaceIfOld(fresh, time.Now().Add(-40*day)) // this node's view of the file is stale
	if after, _, _ := readTokenFile(path); got != fresh || after != fresh {
		t.Errorf("store %q, file %q, want both to stay %q", got, after, fresh)
	}
}

func TestReplaceTokenFileRewritesAnyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.token")
	if err := os.WriteFile(path, []byte("short\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := replaceTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, mod, err := readTokenFile(path)
	if err != nil || got != tok {
		t.Fatalf("read back %q, %v; want %q", got, err, tok)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 || time.Since(mod) > time.Minute {
		t.Errorf("mode %v, age %v: want 0600 and new", info.Mode().Perm(), time.Since(mod))
	}
}

// A token file whose read never ends must not hold up a request. A FIFO with no writer blocks open() the way a stalled mount does.
func TestStalledTokenFileDoesNotBlockRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.token")
	store := newTokenStore(path, quietLog(), never)
	if err := store.ensure(); err != nil {
		t.Fatal(err)
	}
	tok, _, _ := readTokenFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	t.Cleanup(func() { // release the blocked read
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			f.WriteString("short\n") //nolint:errcheck
			f.Close()
		}
		settle(store)
	})
	store.mu.Lock()
	store.loadedAt = time.Time{}
	store.mu.Unlock()

	start := time.Now()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !store.check(tok) {
				t.Error("a right token was refused while the file read stalled")
			}
		}()
	}
	wg.Wait()
	// One request pays the short wait for the read, and the others are answered from memory.
	if d := time.Since(start); d > time.Second {
		t.Errorf("20 requests took %v while the file read stalled, want about %v", d, tokenFreshWait)
	}
	start = time.Now()
	for range 20 {
		if !store.check(tok) {
			t.Fatal("a right token was refused while the file read stalled")
		}
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("20 later requests took %v, want them answered from memory", d)
	}
	// A wrong token waits for the read it started, then gives up.
	start = time.Now()
	if store.check("wrong-wrong-wrong-wrong") {
		t.Error("a wrong token was accepted")
	}
	if d := time.Since(start); d > 2*tokenMissWait {
		t.Errorf("a wrong token waited %v, want about %v", d, tokenMissWait)
	}
}

// The first request after a quiet spell is judged by the file, not by the token the server held while it was idle.
func TestRequestAfterAQuietSpellSeesAReplacedToken(t *testing.T) {
	store, path, old := ageStore(t, time.Minute, 0)
	if err := store.ensure(); err != nil {
		t.Fatal(err)
	}
	fresh, err := replaceTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.loadedAt, store.missAt = time.Now().Add(-time.Hour), time.Now()
	store.mu.Unlock()
	if store.check(old) {
		t.Error("the first request after a quiet spell accepted a token the file no longer holds")
	}
	if !store.check(fresh) {
		t.Error("the new token was refused")
	}
}
