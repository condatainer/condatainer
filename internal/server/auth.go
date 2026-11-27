package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/logging"
)

const (
	// tokenParam is the query parameter that carries the token in a sign-in link.
	tokenParam = "cnt_token"
	// ticketParam is the query parameter that carries a one-time ticket from the dashboard to a helper origin.
	ticketParam = "cnt_ticket"
	// tokenCookiePrefix starts the name of the sign-in cookie, which ends with the port the browser used.
	tokenCookiePrefix = "cnt_token_"
	// tokenCookieAge is the age of the sign-in cookie on the dashboard and on a name origin.
	tokenCookieAge = 400 * 24 * time.Hour
)

const signInRequired = "Sign in required. Run `condatainer server status` on the login node, then open the link it prints.\n"

// tokenCookieName is the sign-in cookie for the port the browser reached the server on.
// Cookies ignore the port, so the port in the name keeps two forwarded servers apart.
func tokenCookieName(port int) string {
	return tokenCookiePrefix + strconv.Itoa(port)
}

// requireToken lets a request through only when it carries the token, for every route and every helper.
//   - A request with ?cnt_ticket= redeems the ticket, sets the sign-in cookie of its host and answers the hop page to the same URL without the parameter.
//   - A request with ?cnt_token= sets the sign-in cookie of its host and redirects to the same URL without the parameter.
//   - Any other request needs the cookie. Without it the answer is 401.
//   - With server.require_token off every request passes, and a ticket is only dropped through the hop page.
func (s *srv) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required := keyRequireToken.Get()
		if required {
			if err := s.tokens.ensure(); err != nil {
				logging.FromContext(s.ctx).Error("server: token unavailable", "error", err)
				http.Error(w, "the server token is not available: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if id, rest, ok := splitQueryParam(r.URL.RawQuery, ticketParam); ok {
			if required {
				if !s.tickets.redeem(id, r.Host, time.Now()) {
					denySignIn(w)
					return
				}
				s.setSignInCookie(w, r, s.tokens.current())
			}
			serveHop(w, selfLocation(r, rest))
			return
		}
		if !required {
			next.ServeHTTP(w, r)
			return
		}
		if candidate, rest, ok := splitQueryParam(r.URL.RawQuery, tokenParam); ok {
			if !s.tokens.check(candidate) {
				denySignIn(w)
				return
			}
			s.setSignInCookie(w, r, candidate)
			w.Header().Set("Location", selfLocation(r, rest))
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.WriteHeader(http.StatusFound)
			return
		}
		if c, err := r.Cookie(tokenCookieName(s.requestPort(r))); err == nil && s.tokens.check(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		denySignIn(w)
	})
}

// denySignIn answers 401 with the line that says how to sign in.
func denySignIn(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(signInRequired)) //nolint:errcheck
}

// setSignInCookie sets the sign-in cookie of the request's host.
func (s *srv) setSignInCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     tokenCookieName(s.requestPort(r)),
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.tokenCookieLife(r.Host) / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// selfLocation is the request's own path with the query rest, for a sign-in to go on to.
// Leading slashes of the path collapse to one, so the location is never read as another host.
func selfLocation(r *http.Request, rest string) string {
	loc := "/" + strings.TrimLeft(r.URL.EscapedPath(), "/")
	if rest != "" {
		loc += "?" + rest
	}
	return loc
}

// tokenCookieLife is how long the sign-in cookie of host lives.
//   - An id origin is never opened again after its run, so its cookie ends with the run, as a helper's own cookies do.
//   - The dashboard and a name origin keep the full age. An origin that names no helper gets the age of a run with no known end.
func (s *srv) tokenCookieLife(host string) time.Duration {
	label := subdomainLabel(host)
	if label == "" {
		return tokenCookieAge
	}
	if e := s.proxies.Get(label); e != nil {
		return idCookieCap(e.end, time.Now())
	}
	if s.proxies.GetByName(label) != nil {
		return tokenCookieAge
	}
	return idCookieNoWalltime
}

// splitQueryParam takes the parameter name out of a raw query and returns its value.
// rest is the query without it, in its original spelling. ok is false when the query has no such parameter.
func splitQueryParam(rawQuery, name string) (value, rest string, ok bool) {
	if rawQuery == "" {
		return "", "", false
	}
	var kept []string
	for _, pair := range strings.Split(rawQuery, "&") {
		key, val, _ := strings.Cut(pair, "=")
		if k, err := url.QueryUnescape(key); err == nil && k == name {
			if v, err := url.QueryUnescape(val); err == nil && !ok {
				value = v
			}
			ok = true
			continue
		}
		kept = append(kept, pair)
	}
	return value, strings.Join(kept, "&"), ok
}

// stripTokenCookies removes the server's sign-in cookies from a request's Cookie headers.
// The other cookies keep their original spelling.
func stripTokenCookies(h http.Header) {
	lines := h.Values("Cookie")
	if len(lines) == 0 {
		return
	}
	var kept []string
	for _, line := range lines {
		for _, c := range strings.Split(line, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(c), "=")
			if strings.HasPrefix(name, tokenCookiePrefix) {
				continue
			}
			if c = strings.TrimSpace(c); c != "" {
				kept = append(kept, c)
			}
		}
	}
	if len(kept) == 0 {
		h.Del("Cookie")
		return
	}
	h.Set("Cookie", strings.Join(kept, "; "))
}
