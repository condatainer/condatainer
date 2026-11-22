package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ticketLife is how long a ticket from /go can be redeemed.
const ticketLife = 30 * time.Second

// ticketStore holds the one-time tickets that sign a browser in to a helper origin.
// A ticket is bound to the host it was minted for and lives in this process only.
type ticketStore struct {
	mu      sync.Mutex
	tickets map[string]ticket
}

type ticket struct {
	host    string
	expires time.Time
}

func newTicketStore() *ticketStore {
	return &ticketStore{tickets: make(map[string]ticket)}
}

// mint returns a new ticket for host, and forgets the ones that expired.
func (ts *ticketStore) mint(host string, now time.Time) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for k, t := range ts.tickets {
		if !now.Before(t.expires) {
			delete(ts.tickets, k)
		}
	}
	ts.tickets[id] = ticket{host: strings.ToLower(host), expires: now.Add(ticketLife)}
	return id, nil
}

// redeem reports whether id is a live ticket for host, and uses it up.
func (ts *ticketStore) redeem(id, host string, now time.Time) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.tickets[id]
	delete(ts.tickets, id)
	return ok && now.Before(t.expires) && t.host == strings.ToLower(host)
}

// handleGo serves GET /go?url=<helper link>, for a click on a helper link in the dashboard.
//   - The link must be http://{label}.localhost:{port}/... on the port the browser used for the dashboard.
//   - The answer is a redirect to the link with a one-time ticket, which the helper's origin trades for its own cookie.
//   - With the token off the redirect goes to the link as it is.
func (s *srv) handleGo(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || subdomainLabel(u.Host) == "" || hostPort(u) != s.requestPort(r) {
		http.Error(w, "not a helper link of this server", http.StatusBadRequest)
		return
	}
	target := raw
	if keyRequireToken.Get() {
		id, err := s.tickets.mint(u.Host, time.Now())
		if err != nil {
			http.Error(w, "cannot make a ticket", http.StatusInternalServerError)
			return
		}
		target = addQueryParam(raw, ticketParam, id)
	}
	w.Header().Set("Location", target)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusFound)
}

// hostPort is the port of a URL, 80 when it names none.
func hostPort(u *url.URL) int {
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
		return -1
	}
	return 80
}

// addQueryParam appends name=value to the query of a URL, before any fragment, and leaves the rest as written.
func addQueryParam(raw, name, value string) string {
	frag := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw, frag = raw[:i], raw[i:]
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + name + "=" + value + frag
}
