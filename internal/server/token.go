package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

const (
	// tokenMinLen is the shortest token a file may hold.
	tokenMinLen = 16
	// tokenRecheck is the longest a server goes without starting a read of the token file while it serves requests.
	tokenRecheck = 3 * time.Second
	// tokenMissRecheck is the least time between two reads that a mismatching request forces.
	tokenMissRecheck = time.Second
	// tokenMissWait is the longest a mismatching request waits for the read it started.
	tokenMissWait = time.Second
	// tokenFileLimit is the most bytes read from the token file.
	tokenFileLimit = 4096
)

// ErrTokenInvalid marks a token file whose content or permissions the server refuses.
var ErrTokenInvalid = errors.New("invalid server token")

// newToken returns 12 random bytes written in base64url, which is 16 URL-safe characters.
// 96 bits cannot be guessed through requests to a port, and the short token keeps a printed link on one line.
func newToken() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validateToken trims a trailing newline and checks the length and the characters of a token.
// A token is URL-safe: letters, digits and . _ ~ -
func validateToken(text string) (string, error) {
	tok := strings.TrimRight(text, "\r\n")
	if len(tok) < tokenMinLen {
		return "", fmt.Errorf("%w: it has %d characters, at least %d are needed", ErrTokenInvalid, len(tok), tokenMinLen)
	}
	for _, c := range tok {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._~-", c)
		if !ok {
			return "", fmt.Errorf("%w: it has a character outside letters, digits and . _ ~ -", ErrTokenInvalid)
		}
	}
	return tok, nil
}

// readTokenFile reads the token from path, and the time the file was last written.
//   - A missing file returns an error that matches fs.ErrNotExist.
//   - A file readable by group or others is refused.
//   - The time comes from the open file, so it is not served from a cache of attributes.
func readTokenFile(path string) (string, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", time.Time{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", time.Time{}, fmt.Errorf("%w: %s is readable by group or others; run chmod 600 on it", ErrTokenInvalid, path)
	}
	data, err := io.ReadAll(io.LimitReader(f, tokenFileLimit))
	if err != nil {
		return "", time.Time{}, err
	}
	tok, err := validateToken(string(data))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%s: %w", path, err)
	}
	return tok, info.ModTime(), nil
}

// writeTokenTemp writes a new random token to a file of its own beside path, mode 0600.
// The caller moves the file into place and removes it.
func writeTokenTemp(path string) (tok, tmpName string, err error) {
	dir := filepath.Dir(path)
	if !utils.EnsureWritableDir(dir) {
		return "", "", fmt.Errorf("cannot create state directory: %s", dir)
	}
	if tok, err = newToken(); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, "server.token.tmp-*")
	if err != nil {
		return "", "", err
	}
	_, werr := tmp.WriteString(tok + "\n")
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name()) //nolint:errcheck
		return "", "", werr
	}
	return tok, tmp.Name(), nil
}

// createTokenFile writes a new random token to path, mode 0600, and returns it.
//   - The file is made under a temporary name and linked into place, which fails when path exists.
//   - A server that loses the race to create it returns the token the winner wrote.
func createTokenFile(path string) (string, error) {
	tok, tmp, err := writeTokenTemp(path)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp) //nolint:errcheck
	if err := os.Link(tmp, path); err != nil {
		if winner, _, rerr := readTokenFile(path); rerr == nil {
			return winner, nil
		}
		return "", err
	}
	return tok, nil
}

// replaceTokenFile writes a new random token over path, whatever it holds, and returns it.
// The new file is moved into place in one step, so a reader sees the old token or the new one.
func replaceTokenFile(path string) (string, error) {
	tok, tmp, err := writeTokenTemp(path)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return "", err
	}
	return tok, nil
}

// TokenRequired reports whether the dashboard asks for its token.
func TokenRequired() bool { return keyRequireToken.Get() }

// RotateToken replaces the user's token file with a new random token. A running server follows the file.
func RotateToken() (string, error) {
	return replaceTokenFile(config.GetServerTokenPath())
}

// tokenStore holds the token a server checks requests against.
//   - It follows the file. A request that finds the last read older than tokenRecheck starts a read in the background and is judged by the token the store has.
//   - A request that does not match starts a read too, and waits up to tokenMissWait for it, so a token just written works at once.
//   - A read runs outside the lock, one at a time. A file that stalls leaves every request answered from memory.
//   - A file that is missing, unreadable or refused leaves the token the store already has.
type tokenStore struct {
	path   string
	log    *slog.Logger
	maxAge func() time.Duration // the age that replaces the token; zero never does

	mu         sync.Mutex
	token      string
	loadedAt   time.Time     // when the last read started
	missAt     time.Time     // when the last read that a mismatching request forced started
	lastErr    string        // last read error that was logged
	refreshing chan struct{} // closed when the read in progress ends; nil when none runs
}

func newTokenStore(path string, log *slog.Logger, maxAge func() time.Duration) *tokenStore {
	return &tokenStore{path: path, log: log, maxAge: maxAge}
}

// ensure loads the token, creating the file when there is none. It returns the reason when the file is refused.
// The first load holds the lock, because there is no token to answer from until it ends.
func (t *tokenStore) ensure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" {
		return nil
	}
	tok, mod, err := readTokenFile(t.path)
	if errors.Is(err, fs.ErrNotExist) {
		tok, err = createTokenFile(t.path)
		mod = time.Now()
	}
	if err != nil {
		return err
	}
	t.token, t.loadedAt = t.replaceIfOld(tok, mod), time.Now()
	return nil
}

// replaceIfOld returns the token to use for a file holding tok, last written at mod. It writes a new token when the file is older than the configured age.
//   - The file is read again first. When another node already replaced it, that token is taken.
//   - Two nodes that replace together each write a token. The last one wins and both read it back within a few seconds.
func (t *tokenStore) replaceIfOld(tok string, mod time.Time) string {
	limit := t.maxAge()
	if limit <= 0 || time.Since(mod) <= limit {
		return tok
	}
	if tok2, mod2, err := readTokenFile(t.path); err == nil && time.Since(mod2) <= limit {
		return tok2
	}
	fresh, err := replaceTokenFile(t.path)
	if err != nil {
		t.log.Warn("server: cannot replace the old token", "error", err)
		return tok
	}
	t.log.Info("server: the token was older than server.token_days and was replaced")
	return fresh
}

// startRefresh starts a read of the file unless one is running, and returns the channel that closes when it ends.
// The caller holds mu.
func (t *tokenStore) startRefresh(now time.Time) <-chan struct{} {
	if t.refreshing != nil {
		return t.refreshing
	}
	done := make(chan struct{})
	t.refreshing = done
	t.loadedAt = now
	go func() {
		defer close(done)
		tok, mod, err := readTokenFile(t.path)
		if err == nil {
			tok = t.replaceIfOld(tok, mod)
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		t.refreshing = nil
		switch {
		case err == nil:
			t.token, t.lastErr = tok, ""
		case errors.Is(err, fs.ErrNotExist):
			// A deleted file keeps the token the server has.
		case err.Error() != t.lastErr:
			t.lastErr = err.Error()
			t.log.Warn("server: token file not used, keeping the current token", "error", err)
		}
	}()
	return done
}

// check reports whether candidate is the token. The comparison takes the same time for any candidate of one length.
func (t *tokenStore) check(candidate string) bool {
	if candidate == "" {
		return false
	}
	t.mu.Lock()
	if t.token == "" {
		t.mu.Unlock()
		return false
	}
	now := time.Now()
	if now.Sub(t.loadedAt) >= tokenRecheck {
		t.startRefresh(now)
	}
	if t.matches(candidate) {
		t.mu.Unlock()
		return true
	}
	if now.Sub(t.missAt) < tokenMissRecheck {
		t.mu.Unlock()
		return false
	}
	t.missAt = now
	done := t.startRefresh(now)
	t.mu.Unlock()

	select {
	case <-done:
	case <-time.After(tokenMissWait):
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.matches(candidate)
}

func (t *tokenStore) matches(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(t.token)) == 1
}
