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

	"github.com/condatainer/condatainer/internal/utils"
)

const (
	// tokenMinLen is the shortest token a file may hold.
	tokenMinLen = 16
	// tokenRecheck is the longest a server goes without reading the token file while it serves requests.
	tokenRecheck = 3 * time.Second
	// tokenMissRecheck is the least time between two reads that a mismatching request forces.
	tokenMissRecheck = time.Second
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

// readTokenFile reads the token from path. A missing file returns an error that matches fs.ErrNotExist.
//   - A file readable by group or others is refused.
func readTokenFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: %s is readable by group or others; run chmod 600 on it", ErrTokenInvalid, path)
	}
	data, err := io.ReadAll(io.LimitReader(f, tokenFileLimit))
	if err != nil {
		return "", err
	}
	tok, err := validateToken(string(data))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return tok, nil
}

// createTokenFile writes a new random token to path, mode 0600, and returns it.
//   - The file is made under a temporary name and linked into place, which fails when path exists.
//   - A server that loses the race to create it returns the token the winner wrote.
func createTokenFile(path string) (string, error) {
	dir := filepath.Dir(path)
	if !utils.EnsureWritableDir(dir) {
		return "", fmt.Errorf("cannot create state directory: %s", dir)
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "server.token.tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	_, werr := tmp.WriteString(tok + "\n")
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if winner, rerr := readTokenFile(path); rerr == nil {
			return winner, nil
		}
		return "", err
	}
	return tok, nil
}

// tokenStore holds the token a server checks requests against.
//   - It follows the file: a read at least every tokenRecheck while requests arrive, and a read when a request does not match.
//   - A file that is missing, unreadable or refused leaves the token the store already has.
type tokenStore struct {
	path string
	log  *slog.Logger

	mu       sync.Mutex
	token    string
	loadedAt time.Time // last read of the file
	missAt   time.Time // last read that a mismatching request forced
	lastErr  string    // last read error that was logged
}

func newTokenStore(path string, log *slog.Logger) *tokenStore {
	return &tokenStore{path: path, log: log}
}

// ensure loads the token, creating the file when there is none. It returns the reason when the file is refused.
func (t *tokenStore) ensure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.load()
}

// load reads the file into an empty store, creating the file when it does not exist. The caller holds mu.
func (t *tokenStore) load() error {
	if t.token != "" {
		return nil
	}
	tok, err := readTokenFile(t.path)
	if errors.Is(err, fs.ErrNotExist) {
		tok, err = createTokenFile(t.path)
	}
	if err != nil {
		return err
	}
	t.token, t.loadedAt = tok, time.Now()
	return nil
}

// refresh reads the file again and keeps the old token when it cannot use what it finds. The caller holds mu.
func (t *tokenStore) refresh(now time.Time) {
	t.loadedAt = now
	tok, err := readTokenFile(t.path)
	switch {
	case err == nil:
		t.token, t.lastErr = tok, ""
	case errors.Is(err, fs.ErrNotExist):
		// A deleted file keeps the token the server has.
	case err.Error() != t.lastErr:
		t.lastErr = err.Error()
		t.log.Warn("server: token file not used, keeping the current token", "error", err)
	}
}

// check reports whether candidate is the token. The comparison takes the same time for any candidate of one length.
func (t *tokenStore) check(candidate string) bool {
	if candidate == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.load(); err != nil {
		return false
	}
	now := time.Now()
	if now.Sub(t.loadedAt) >= tokenRecheck {
		t.refresh(now)
	}
	if t.matches(candidate) {
		return true
	}
	if now.Sub(t.missAt) < tokenMissRecheck {
		return false
	}
	t.missAt = now
	t.refresh(now)
	return t.matches(candidate)
}

func (t *tokenStore) matches(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(t.token)) == 1
}
