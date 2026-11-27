package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestTicketWorksOnceForItsHostBeforeItExpires(t *testing.T) {
	ts := newTicketStore()
	now := time.Now()
	id, err := ts.mint("A.localhost:1", now)
	if err != nil {
		t.Fatal(err)
	}
	if ts.redeem(id, "b.localhost:1", now) {
		t.Error("a ticket was accepted for another host")
	}
	// A refused redemption used the ticket up, so a guess cannot be retried.
	if ts.redeem(id, "a.localhost:1", now) {
		t.Error("a ticket survived a refused redemption")
	}
	id, _ = ts.mint("a.localhost:1", now)
	if !ts.redeem(id, "A.localhost:1", now.Add(ticketLife-time.Second)) {
		t.Error("a live ticket was refused (the host compares without case)")
	}
	if ts.redeem(id, "a.localhost:1", now) {
		t.Error("a ticket worked twice")
	}
	id, _ = ts.mint("a.localhost:1", now)
	if ts.redeem(id, "a.localhost:1", now.Add(ticketLife)) {
		t.Error("an expired ticket was accepted")
	}
	if ts.redeem("never-minted", "a.localhost:1", now) {
		t.Error("an unknown ticket was accepted")
	}
}

func TestTicketsThatExpiredAreForgotten(t *testing.T) {
	ts := newTicketStore()
	now := time.Now()
	ts.mint("a.localhost:1", now)                   //nolint:errcheck
	ts.mint("b.localhost:1", now.Add(2*ticketLife)) //nolint:errcheck
	if len(ts.tickets) != 1 {
		t.Errorf("%d tickets held, want only the live one", len(ts.tickets))
	}
}

// A click on a helper link: /go on the dashboard, then the helper origin trades the ticket for its own cookie.
func TestGoSignsTheBrowserInToTheHelperOrigin(t *testing.T) {
	a := newAuthServer(t)
	const helperHost = "code-20260101-000000.localhost:44449"
	link := "http://" + helperHost + "/lab?token=jup"

	rec := a.do("localhost:44449", "/go?url="+url.QueryEscape(link), a.cookie())
	if rec.Code != http.StatusFound {
		t.Fatalf("/go status %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, link+"&cnt_ticket=") || strings.Contains(loc, a.token) {
		t.Fatalf("Location = %q, want the link plus a ticket and no token", loc)
	}
	u, _ := url.Parse(loc)

	// The helper origin has no cookie. The ticket signs it in and goes away through the hop page.
	rec = a.do(u.Host, u.RequestURI())
	if rec.Code != http.StatusOK || hopTarget(rec) != "/lab?token=jup" {
		t.Fatalf("exchange: status %d, hop to %q; want 200 going on to the clean link with the helper's own token", rec.Code, hopTarget(rec))
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || strings.Contains(rec.Body.String(), a.token) {
		t.Error("the hop page leaks a Referer or carries the token")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "cnt_token_44449" || cookies[0].Value != a.token {
		t.Fatalf("cookies = %v", cookies)
	}
	if rec = a.do(u.Host, u.RequestURI()); rec.Code != http.StatusUnauthorized {
		t.Errorf("a ticket used twice: status %d, want 401", rec.Code)
	}
	if rec = a.do(helperHost, "/lab?token=jup", cookies[0]); rec.Code != http.StatusOK || rec.Body.String() != "helper" {
		t.Errorf("after sign-in: %d %q", rec.Code, rec.Body.String())
	}
}

func TestGoNeedsTheDashboardSignIn(t *testing.T) {
	a := newAuthServer(t)
	link := "http://code-20260101-000000.localhost:44449/"
	if rec := a.do("localhost:44449", "/go?url="+url.QueryEscape(link)); rec.Code != http.StatusUnauthorized {
		t.Errorf("/go without a cookie: status %d, want 401", rec.Code)
	}
}

func TestGoRefusesAnythingButAHelperLinkOfThisServer(t *testing.T) {
	a := newAuthServer(t)
	for name, target := range map[string]string{
		"no url":         "",
		"other site":     "http://example.com:44449/",
		"https":          "https://code.localhost:44449/",
		"the dashboard":  "http://localhost:44449/",
		"other port":     "http://code.localhost:5555/",
		"no port":        "http://code.localhost/",
		"user info":      "http://user@code.localhost:44449/",
		"not a URL":      "::::",
		"relative":       "/lab",
		"ip of the host": "http://127.0.0.1:44449/",
	} {
		rec := a.do("localhost:44449", "/go?url="+url.QueryEscape(target), a.cookie())
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestGoWithTheTokenOffStillHopsAndSetsNoCookie(t *testing.T) {
	a := newAuthServer(t)
	settingstest.Override(t, "server.require_token", "false")
	link := "http://code.localhost:44449/x?a=1#f"
	rec := a.do("localhost:44449", "/go?url="+url.QueryEscape(link))
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.HasPrefix(loc, "http://code.localhost:44449/x?a=1&cnt_ticket=") || !strings.HasSuffix(loc, "#f") {
		t.Fatalf("/go: status %d, Location %q; want 302 to the link with a ticket", rec.Code, loc)
	}
	u, _ := url.Parse(loc)
	rec = a.do(u.Host, u.RequestURI())
	if rec.Code != http.StatusOK || hopTarget(rec) != "/x?a=1" || len(rec.Result().Cookies()) != 0 {
		t.Errorf("helper origin: status %d, hop to %q, cookies %v; want 200 going on to /x?a=1 with no cookie", rec.Code, hopTarget(rec), rec.Result().Cookies())
	}
}

func TestHopPageEscapesTheLocation(t *testing.T) {
	rec := httptest.NewRecorder()
	serveHop(rec, `/x?a=</script><b>"&c`)
	body := rec.Body.String()
	if strings.Contains(body, "</script><b>") || strings.Contains(body, `<b>"`) {
		t.Errorf("the location reaches the page unescaped: %s", body)
	}
	if hopTarget(rec) != `/x?a=</script><b>"&c` {
		t.Errorf("hop to %q", hopTarget(rec))
	}
}

// hopTarget is the location a hop page goes on to, or "" when the body is not one.
func hopTarget(rec *httptest.ResponseRecorder) string {
	body := rec.Body.String()
	const pre = "location.replace("
	i := strings.Index(body, pre)
	if i < 0 {
		return ""
	}
	var loc string
	if err := json.NewDecoder(strings.NewReader(body[i+len(pre):])).Decode(&loc); err != nil {
		return ""
	}
	return loc
}

func TestAddQueryParamKeepsTheFragmentAndTheRest(t *testing.T) {
	for in, want := range map[string]string{
		"http://h/":           "http://h/?k=v",
		"http://h/p?a=b%2Fc":  "http://h/p?a=b%2Fc&k=v",
		"http://h/p?a=1#frag": "http://h/p?a=1&k=v#frag",
		"http://h/p#frag":     "http://h/p?k=v#frag",
	} {
		if got := addQueryParam(in, "k", "v"); got != want {
			t.Errorf("addQueryParam(%q) = %q, want %q", in, got, want)
		}
	}
}
