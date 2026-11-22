package server

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

const testPort = 44449

// authServer is a server whose dashboard answers "dash" and whose helper "code-20260101-000000"
// (name "code") answers with what it received, behind the token check.
type authServer struct {
	s     *srv
	h     http.Handler
	token string
	seen  chan *http.Request
}

func newAuthServer(t *testing.T) *authServer {
	t.Helper()
	settingstest.Override(t, "server.require_token", "true")
	a := &authServer{seen: make(chan *http.Request, 8)}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.seen <- r.Clone(context.Background())
		w.Write([]byte("helper")) //nolint:errcheck
	}))
	t.Cleanup(backend.Close)
	target, _ := url.Parse(backend.URL)

	log := quietLog()
	s := &srv{
		port:    testPort,
		ctx:     context.Background(),
		proxies: newProxyRegistry(log),
		tickets: newTicketStore(),
		tokens:  newTokenStore(filepath.Join(t.TempDir(), "server.token"), log, never),
	}
	s.proxies.entries["code-20260101-000000"] = &proxyEntry{
		rp:   newReverseProxy(target, nil, func() {}),
		name: "code",
		end:  time.Now().Add(30 * time.Hour),
	}
	if err := s.tokens.ensure(); err != nil {
		t.Fatal(err)
	}
	a.s = s
	a.token, _, _ = readTokenFile(s.tokens.path)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("dash")) }) //nolint:errcheck
	mux.HandleFunc("/go", s.handleGo)
	a.h = s.requireToken(s.hostDispatch(mux))
	return a
}

func (a *authServer) do(host, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "http://"+host+target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a *authServer) cookie() *http.Cookie {
	return &http.Cookie{Name: tokenCookieName(testPort), Value: a.token}
}

func TestEveryRouteNeedsTheToken(t *testing.T) {
	a := newAuthServer(t)
	dash, helperHost := "localhost:44449", "code-20260101-000000.localhost:44449"
	wrong := &http.Cookie{Name: tokenCookieName(testPort), Value: "wrong-wrong-wrong-wrong"}
	for _, host := range []string{dash, helperHost} {
		for _, target := range []string{"/", "/static/app.js", "/api/status"} {
			for name, rec := range map[string]*httptest.ResponseRecorder{
				"no cookie":        a.do(host, target),
				"wrong cookie":     a.do(host, target, wrong),
				"wrong parameter":  a.do(host, target+"?cnt_token=wrong-wrong-wrong-wrong"),
				"other port named": a.do(host, target, &http.Cookie{Name: tokenCookieName(1), Value: a.token}),
			} {
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("%s %s%s: status %d, want 401", name, host, target, rec.Code)
				}
				if !strings.Contains(rec.Body.String(), "condatainer server status") {
					t.Errorf("%s %s%s: the 401 body does not say how to sign in: %q", name, host, target, rec.Body.String())
				}
			}
		}
	}
	if got := a.do(dash, "/api/status", a.cookie()); got.Code != http.StatusOK || got.Body.String() != "dash" {
		t.Errorf("a right cookie on the dashboard: %d %q", got.Code, got.Body.String())
	}
	if got := a.do(helperHost, "/x", a.cookie()); got.Code != http.StatusOK || got.Body.String() != "helper" {
		t.Errorf("a right cookie on a helper: %d %q", got.Code, got.Body.String())
	}
}

func TestSignInSetsTheCookieAndDropsOnlyTheToken(t *testing.T) {
	a := newAuthServer(t)
	// A helper's own token parameter stays, in its original spelling.
	rec := a.do("localhost:44449", "/lab?a=1&cnt_token="+a.token+"&token=j%2Fup")
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/lab?a=1&token=j%2Fup" {
		t.Errorf("Location = %q, want the URL without cnt_token", loc)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("the redirect does not stop the Referer")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies set, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != "cnt_token_44449" || c.Value != a.token || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie = %+v", c)
	}
	if c.MaxAge != 400*24*3600 {
		t.Errorf("dashboard cookie Max-Age = %d, want 400 days", c.MaxAge)
	}
	if strings.Contains(rec.Body.String(), a.token) {
		t.Error("the response body carries the token")
	}
}

func TestSignInCookieNameFollowsTheBrowsersPort(t *testing.T) {
	a := newAuthServer(t)
	rec := a.do("localhost:9999", "/?cnt_token="+a.token)
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != "cnt_token_9999" {
		t.Errorf("cookies = %v, want one named cnt_token_9999", c)
	}
}

func TestSignInCookieAgeOnHelperOrigins(t *testing.T) {
	a := newAuthServer(t)
	age := func(host string) int {
		rec := a.do(host, "/?cnt_token="+a.token)
		c := rec.Result().Cookies()
		if len(c) != 1 {
			t.Fatalf("%s: %d cookies", host, len(c))
		}
		return c[0].MaxAge
	}
	// 30 h walltime end ahead: the id origin gets the time left, a little under 108000 s.
	if got := age("code-20260101-000000.localhost:44449"); got < 107000 || got > 108000 {
		t.Errorf("id origin Max-Age = %d, want the time left in the walltime (about 108000)", got)
	}
	if got := age("code.localhost:44449"); got != 400*24*3600 {
		t.Errorf("name origin Max-Age = %d, want 400 days", got)
	}
	if got := age("unknown.localhost:44449"); got != int(idCookieNoWalltime/time.Second) {
		t.Errorf("unknown origin Max-Age = %d, want 7 days", got)
	}
}

func TestSignInRedirectStaysOnTheHost(t *testing.T) {
	a := newAuthServer(t)
	rec := a.do("localhost:44449", "//evil.example/x?cnt_token="+a.token)
	if loc := rec.Header().Get("Location"); loc != "/evil.example/x" {
		t.Errorf("Location = %q, want a path on the same host", loc)
	}
}

func TestProxyKeepsTheServersCookieAndTokenFromTheHelper(t *testing.T) {
	a := newAuthServer(t)
	req := httptest.NewRequest("GET", "http://code-20260101-000000.localhost:44449/lab?token=jup", nil)
	req.AddCookie(a.cookie())
	req.AddCookie(&http.Cookie{Name: "cnt_token_7", Value: "other-port-token"})
	req.AddCookie(&http.Cookie{Name: "app", Value: "keep"})
	a.h.ServeHTTP(httptest.NewRecorder(), req)
	got := <-a.seen
	if c := got.Header.Get("Cookie"); c != "app=keep" {
		t.Errorf("the helper received Cookie %q, want only app=keep", c)
	}
	if got.URL.RawQuery != "token=jup" {
		t.Errorf("the helper received query %q, want its own token unchanged", got.URL.RawQuery)
	}
}

func TestTokenOffLetsEveryRouteThroughAndCreatesNoFile(t *testing.T) {
	a := newAuthServer(t)
	path := filepath.Join(t.TempDir(), "never.token")
	a.s.tokens = newTokenStore(path, quietLog(), never)
	settingstest.Override(t, "server.require_token", "false")
	if rec := a.do("localhost:44449", "/api/status"); rec.Code != http.StatusOK {
		t.Errorf("status %d with the token off, want 200", rec.Code)
	}
	if rec := a.do("code-20260101-000000.localhost:44449", "/"); rec.Code != http.StatusOK {
		t.Errorf("helper status %d with the token off, want 200", rec.Code)
	}
	if _, _, err := readTokenFile(path); err == nil {
		t.Error("a token file was created with the token off")
	}
	settingstest.Override(t, "server.require_token", "true")
	if rec := a.do("localhost:44449", "/api/status"); rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d after turning the token on, want 401", rec.Code)
	}
}

func TestSplitQueryParam(t *testing.T) {
	tests := []struct {
		in, token, rest string
		ok              bool
	}{
		{"", "", "", false},
		{"a=1", "", "a=1", false},
		{"cnt_token=T", "T", "", true},
		{"a=1&cnt_token=T&b=2", "T", "a=1&b=2", true},
		{"token=jup&cnt_token=T", "T", "token=jup", true},
		{"cnt_token=A&cnt_token=B", "A", "", true},
		{"mycnt_token=T", "", "mycnt_token=T", false},
	}
	for _, tt := range tests {
		token, rest, ok := splitQueryParam(tt.in, tokenParam)
		if token != tt.token || rest != tt.rest || ok != tt.ok {
			t.Errorf("splitQueryParam(%q) = %q, %q, %v; want %q, %q, %v", tt.in, token, rest, ok, tt.token, tt.rest, tt.ok)
		}
	}
}

// A rotation changes what new requests need. A stream that is already open keeps flowing.
func TestRotationLeavesAnOpenStreamConnected(t *testing.T) {
	a := newAuthServer(t)
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Write([]byte("one\n")) //nolint:errcheck
		fl.Flush()
		<-release
		w.Write([]byte("two\n")) //nolint:errcheck
		fl.Flush()
	})
	ts := httptest.NewServer(a.s.requireToken(mux))
	defer ts.Close()
	cookie := &http.Cookie{Name: tokenCookieName(ts.Listener.Addr().(*net.TCPAddr).Port), Value: a.token}
	client := &http.Client{Timeout: 5 * time.Second}
	open := func() *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+"/stream", nil)
		req.AddCookie(cookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	stream := open()
	defer stream.Body.Close()
	rd := bufio.NewReader(stream.Body)
	if line, _ := rd.ReadString('\n'); line != "one\n" {
		t.Fatalf("first chunk = %q", line)
	}

	if _, err := replaceTokenFile(a.s.tokens.path); err != nil {
		t.Fatal(err)
	}
	a.s.tokens.mu.Lock()
	a.s.tokens.loadedAt = time.Time{}
	a.s.tokens.mu.Unlock()
	if resp := open(); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a new stream with the old cookie: status %d, want 401", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	close(release)
	if line, _ := rd.ReadString('\n'); line != "two\n" {
		t.Errorf("the open stream stopped after the rotation: got %q", line)
	}
}
