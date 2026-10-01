package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxIndexSize bounds what a fetch will read, so a wrong URL cannot pull an
// unbounded body into memory.
const maxIndexSize = 32 << 20

// httpBackend reads a source over HTTP, where there is no directory listing and
// no cheap way to read a hundred recipes — hence the generated index.
type httpBackend struct {
	src    *Source
	cache  Cache
	client *http.Client
}

func (h *httpBackend) read(ctx context.Context, path string) ([]byte, error) {
	if data, fresh := h.cache.get(h.src.Base, path); fresh {
		return data, nil
	}

	data, err := h.fetch(ctx, path)
	if err == nil {
		_ = h.cache.put(h.src.Base, path, data)
		return data, nil
	}

	// Expired with no route out is the normal state of a compute node, not an
	// error. Serve what is cached and say that it happened.
	if stale, _ := h.cache.get(h.src.Base, path); stale != nil {
		h.src.Stale = true
		return stale, nil
	}
	return nil, err
}

// fetch reads one file of the source, sending its token when it has one.
//   - A 401 with the token is retried once without it. If that answers, the token is set aside for later reads and TokenRefused is set.
//   - If that is refused too, the error is ErrTokenRefused naming where the token is stored.
func (h *httpBackend) fetch(ctx context.Context, path string) ([]byte, error) {
	token := h.src.Token
	if h.src.TokenRefused {
		token = nil
	}
	data, status, err := h.get(ctx, path, token)
	if status != http.StatusUnauthorized || token == nil {
		return data, err
	}
	data, status, err = h.get(ctx, path, nil)
	switch {
	case err == nil:
		h.src.TokenRefused = true
		return data, nil
	case status == http.StatusUnauthorized || status == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrTokenRefused, token)
	}
	return nil, err
}

// get sends one GET the way the source's host serves a file, with the token
// when set, and reports the status alongside any error.
func (h *httpBackend) get(ctx context.Context, path string, token *Token) ([]byte, int, error) {
	api := hostFor(h.src.Base)
	url := api.fileURL(h.src.Base, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if token != nil {
		api.authorize(req, token.secret)
	}
	resp, err := h.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return nil, resp.StatusCode, fmt.Errorf("%w: %s: %s", ErrUnreadable, url, resp.Status)
	default:
		return nil, resp.StatusCode, fmt.Errorf("catalog: %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexSize))
	return data, resp.StatusCode, err
}

func (h *httpBackend) httpClient() *http.Client {
	if h.client != nil {
		return h.client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// entries reads the generated index, preferring the compressed copy.
func (h *httpBackend) entries(ctx context.Context) (map[string]*Entry, error) {
	data, err := h.readIndex(ctx, indexDir+"/recipes.json")
	if err != nil {
		return nil, err
	}
	var raw map[string]*Entry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("catalog: %s index: %w", h.src.Name, err)
	}
	for name, e := range raw {
		e.Name = name
	}
	return raw, nil
}

// readIndex tries path.gz first, since that is what a collection publishes for
// a fetch, and falls back to the plain file.
func (h *httpBackend) readIndex(ctx context.Context, path string) ([]byte, error) {
	gz, gzErr := h.read(ctx, path+".gz")
	if gzErr == nil {
		if data, err := gunzip(gz); err == nil {
			return data, nil
		}
	}
	data, err := h.read(ctx, path)
	if err != nil && gzErr != nil {
		return nil, gzErr
	}
	return data, err
}

func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, maxIndexSize))
}
