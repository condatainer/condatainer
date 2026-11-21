package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIDCookieCap(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		end  time.Time
		want time.Duration
	}{
		{"time left in the walltime", now.Add(40 * time.Hour), 40 * time.Hour},
		{"less than a day left", now.Add(2 * time.Hour), idCookieFloor},
		{"walltime already over", now.Add(-time.Hour), idCookieFloor},
		{"no walltime", time.Time{}, idCookieNoWalltime},
	}
	for _, tt := range tests {
		if got := idCookieCap(tt.end, now); got != tt.want {
			t.Errorf("%s: cap = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCapSetCookie(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limit := 10 * time.Hour // 36000 s
	farHTTP := now.Add(400 * 24 * time.Hour).Format(http.TimeFormat)
	nearHTTP := now.Add(time.Hour).Format(http.TimeFormat)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"long Max-Age is capped, other attributes stay",
			"sid=abc; Path=/; Max-Age=34560000; HttpOnly; SameSite=Lax",
			"sid=abc; Path=/; Max-Age=36000; HttpOnly; SameSite=Lax"},
		{"short Max-Age is unchanged", "sid=abc; Max-Age=60; Path=/", "sid=abc; Max-Age=60; Path=/"},
		{"deleting cookie is unchanged", "sid=; Max-Age=0; Path=/", "sid=; Max-Age=0; Path=/"},
		{"session cookie is unchanged", "sid=abc; Path=/; HttpOnly", "sid=abc; Path=/; HttpOnly"},
		{"long Expires is capped",
			"sid=abc; Expires=" + farHTTP + "; Path=/",
			"sid=abc; Expires=" + now.Add(limit).Format(http.TimeFormat) + "; Path=/"},
		{"near Expires is unchanged", "sid=abc; Expires=" + nearHTTP, "sid=abc; Expires=" + nearHTTP},
		{"dashed Expires is capped",
			"sid=abc; expires=Fri, 01-Jan-2027 00:00:00 GMT",
			"sid=abc; Expires=" + now.Add(limit).Format(http.TimeFormat)},
		{"Max-Age wins, Expires is left",
			"sid=abc; Expires=" + farHTTP + "; Max-Age=60",
			"sid=abc; Expires=" + farHTTP + "; Max-Age=60"},
		{"attribute names are case-insensitive", "sid=abc; MAX-AGE=999999", "sid=abc; Max-Age=36000"},
		{"unreadable age is unchanged", "sid=abc; Max-Age=soon; Expires=never", "sid=abc; Max-Age=soon; Expires=never"},
	}
	for _, tt := range tests {
		if got := capSetCookie(tt.in, limit, now); got != tt.want {
			t.Errorf("%s:\n got  %q\n want %q", tt.name, got, tt.want)
		}
	}
}

// cookieBackend sets several cookies, one with a very long age.
func cookieBackend(t *testing.T) *url.URL {
	t.Helper()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "a=1; Path=/; Max-Age=34560000; HttpOnly")
		w.Header().Add("Set-Cookie", "b=2; Path=/; Max-Age=60")
		w.Header().Add("Set-Cookie", "c=3; Path=/")
	}))
	t.Cleanup(b.Close)
	u, _ := url.Parse(b.URL)
	return u
}

func TestSubdomainProxyCapsOnlyTheIDLink(t *testing.T) {
	target := cookieBackend(t)
	s := &srv{proxies: newProxyRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	s.proxies.entries["code-server-20260101-000000"] = &proxyEntry{
		rp:   newReverseProxy(target, nil, func() {}),
		name: "code-server",
		end:  time.Now().Add(30 * time.Hour),
	}
	get := func(label string) []string {
		rec := httptest.NewRecorder()
		s.serveSubdomainProxy(rec, httptest.NewRequest("GET", "http://x/", nil), label)
		return rec.Result().Header.Values("Set-Cookie")
	}

	// 30 h left, so the cap is a little under 108000 s.
	byID := get("code-server-20260101-000000")
	if len(byID) != 3 || !strings.HasPrefix(byID[0], "a=1; Path=/; Max-Age=10") || !strings.HasSuffix(byID[0], "; HttpOnly") {
		t.Errorf("the id link did not cap the long cookie: %q", byID)
	}
	if byID[1] != "b=2; Path=/; Max-Age=60" || byID[2] != "c=3; Path=/" {
		t.Errorf("the id link changed a short or session cookie: %q", byID)
	}

	byName := get("code-server")
	want := []string{"a=1; Path=/; Max-Age=34560000; HttpOnly", "b=2; Path=/; Max-Age=60", "c=3; Path=/"}
	if strings.Join(byName, "|") != strings.Join(want, "|") {
		t.Errorf("the name link changed the cookies: %q", byName)
	}
}
