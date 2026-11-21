package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// idCookieFloor is the shortest age a capped cookie gets, so one does not die at once when little walltime is left.
	idCookieFloor = 24 * time.Hour
	// idCookieNoWalltime is the cap for a run whose end is unknown.
	idCookieNoWalltime = 7 * 24 * time.Hour
)

// cookieCapKey is the context key that carries the cookie age cap of a request that came through an id link.
type cookieCapKey struct{}

// idCookieCap is the longest age a helper's cookie may have when it is set through the id link.
//   - It is the time left until end, and at least idCookieFloor.
//   - A zero end gives idCookieNoWalltime.
func idCookieCap(end, now time.Time) time.Duration {
	if end.IsZero() {
		return idCookieNoWalltime
	}
	return max(end.Sub(now), idCookieFloor)
}

// capCookiesResponse is a ModifyResponse func that shortens the age of every
// Set-Cookie line to the cap the request context carries. A request without a cap is left alone.
func capCookiesResponse(resp *http.Response) error {
	limit, ok := resp.Request.Context().Value(cookieCapKey{}).(time.Duration)
	if !ok {
		return nil
	}
	lines := resp.Header.Values("Set-Cookie")
	if len(lines) == 0 {
		return nil
	}
	now := time.Now()
	capped := make([]string, len(lines))
	for i, line := range lines {
		capped[i] = capSetCookie(line, limit, now)
	}
	resp.Header["Set-Cookie"] = capped
	return nil
}

// capSetCookie returns a Set-Cookie line whose Max-Age, or else Expires, is no longer than limit from now.
//   - Every other attribute is kept as it is.
//   - A cookie with a shorter age, a session cookie, and an unreadable age are unchanged.
func capSetCookie(line string, limit time.Duration, now time.Time) string {
	maxSecs := int64(limit / time.Second)
	parts := strings.Split(line, ";")
	var expiresAt = -1 // index of a readable Expires attribute
	var expires time.Time
	hasMaxAge := false
	for i := 1; i < len(parts); i++ {
		attr := strings.TrimSpace(parts[i])
		key, val, _ := strings.Cut(attr, "=")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "max-age":
			n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
			if err != nil {
				continue
			}
			hasMaxAge = true
			if n > maxSecs {
				parts[i] = leadingSpace(parts[i]) + "Max-Age=" + strconv.FormatInt(maxSecs, 10)
			}
		case "expires":
			if t, ok := parseCookieTime(strings.TrimSpace(val)); ok {
				expiresAt, expires = i, t
			}
		}
	}
	// Max-Age decides when it is present, so Expires is edited only without it.
	if !hasMaxAge && expiresAt >= 0 && expires.Sub(now) > limit {
		parts[expiresAt] = leadingSpace(parts[expiresAt]) + "Expires=" + now.Add(limit).UTC().Format(http.TimeFormat)
	}
	return strings.Join(parts, ";")
}

// leadingSpace returns the whitespace that starts s.
func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// parseCookieTime reads an Expires value in the HTTP date forms and the dashed form cookies use.
func parseCookieTime(v string) (time.Time, bool) {
	if t, err := http.ParseTime(v); err == nil {
		return t, true
	}
	if t, err := time.Parse("Mon, 02-Jan-2006 15:04:05 MST", v); err == nil {
		return t, true
	}
	return time.Time{}, false
}
