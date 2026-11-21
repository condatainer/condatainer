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
	// tokenCookiePrefix starts the name of the sign-in cookie, which ends with the port the browser used.
	tokenCookiePrefix = "cnt_token_"
	// tokenCookieAge is the age of the sign-in cookie on the dashboard and on a name origin.
	tokenCookieAge = 400 * 24 * time.Hour
)

const signInRequired = "Sign in required. Open the link that `condatainer server start` prints, or run `condatainer server status` to see it again.\n"

// tokenCookieName is the sign-in cookie for the port the browser reached the server on.
// Cookies ignore the port, so the port in the name keeps two forwarded servers apart.
func tokenCookieName(port int) string {
	return tokenCookiePrefix + strconv.Itoa(port)
}

// requireToken lets a request through only when it carries the token, for every route and every helper.
//   - A request with ?cnt_token= sets the sign-in cookie of its host and redirects to the same URL without the parameter.
//   - Any other request needs the cookie. Without it the answer is 401.
//   - With server.require_token off every request passes.
func (s *srv) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !keyRequireToken.Get() {
			next.ServeHTTP(w, r)
			return
		}
		if err := s.tokens.ensure(); err != nil {
			logging.FromContext(s.ctx).Error("server: token unavailable", "error", err)
			http.Error(w, "the server token is not available: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if candidate, rest, ok := splitTokenParam(r.URL.RawQuery); ok {
			if !s.tokens.check(candidate) {
				denySignIn(w)
				return
			}
			s.signIn(w, r, candidate, rest)
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

// signIn sets the sign-in cookie and redirects to the request's own URL with the remaining query.
//   - The redirect carries no Referer, so the token in the first URL goes no further.
//   - Leading slashes of the path collapse to one, so the Location is never read as another host.
func (s *srv) signIn(w http.ResponseWriter, r *http.Request, token, rest string) {
	http.SetCookie(w, &http.Cookie{
		Name:     tokenCookieName(s.requestPort(r)),
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.tokenCookieLife(r.Host) / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	loc := "/" + strings.TrimLeft(r.URL.EscapedPath(), "/")
	if rest != "" {
		loc += "?" + rest
	}
	w.Header().Set("Location", loc)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusFound)
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

// splitTokenParam takes the token out of a raw query. rest is the query without it, in its original spelling.
// ok is false when the query has no token parameter.
func splitTokenParam(rawQuery string) (token, rest string, ok bool) {
	if rawQuery == "" {
		return "", "", false
	}
	var kept []string
	for _, pair := range strings.Split(rawQuery, "&") {
		key, val, _ := strings.Cut(pair, "=")
		if k, err := url.QueryUnescape(key); err == nil && k == tokenParam {
			if v, err := url.QueryUnescape(val); err == nil && !ok {
				token = v
			}
			ok = true
			continue
		}
		kept = append(kept, pair)
	}
	return token, strings.Join(kept, "&"), ok
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
