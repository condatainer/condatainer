package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readEvent returns the next "data:" line of an SSE body, or "" on timeout.
func readEvent(t *testing.T, sc *bufio.Scanner, within time.Duration) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				got <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
		got <- ""
	}()
	select {
	case line := <-got:
		return line
	case <-time.After(within):
		return ""
	}
}

// TestEventsStream checks that a client gets an event when it connects and
// another after a change, and that a burst of changes is one event.
func TestEventsStream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc((&srv{}).handleEvents))
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-transform") {
		t.Errorf("Cache-Control = %q, want no-transform", cc)
	}
	sc := bufio.NewScanner(resp.Body)

	if got := readEvent(t, sc, 2*time.Second); got != `{"t":"jobs"}` {
		t.Fatalf("event on connect = %q", got)
	}
	notifyJobsChanged()
	notifyJobsChanged()
	notifyJobsChanged()
	if got := readEvent(t, sc, 2*time.Second); got != `{"t":"jobs"}` {
		t.Fatalf("event after a change = %q", got)
	}
	if got := readEvent(t, sc, 3*eventsCoalesce); got != "" {
		t.Fatalf("a burst of changes sent a second event: %q", got)
	}
}
