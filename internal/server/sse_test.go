package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestServeSSEEndsAfterFinalEvent checks that the response ends once the
// final "done" event has been sent, and that the event reaches the client.
func TestServeSSEEndsAfterFinalEvent(t *testing.T) {
	b := newSSEBroker()
	w := httptest.NewRecorder()
	ended := make(chan struct{})
	go func() {
		b.ServeSSE(w, httptest.NewRequest("GET", "/stream", nil))
		close(ended)
	}()

	for { // wait until the handler is subscribed
		b.mu.Lock()
		n := len(b.clients)
		b.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	broadcastDone(b, nil)

	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream stayed open after the final event")
	}
	if got := w.Body.String(); got != "data: {\"ok\":true,\"t\":\"done\"}\n\n" {
		t.Fatalf("body = %q", got)
	}
}

// gatedWriter holds the first write until released, like a client that is
// not reading.
type gatedWriter struct {
	*httptest.ResponseRecorder
	gate  chan struct{}
	first chan struct{}
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	select {
	case <-g.first:
	default:
		close(g.first)
		<-g.gate
	}
	return g.ResponseRecorder.Write(p)
}

var _ http.Flusher = (*gatedWriter)(nil)

// TestServeSSEDeliversFinalToSlowClient checks that a client whose channel
// overflowed still receives the final event and the stream ends.
func TestServeSSEDeliversFinalToSlowClient(t *testing.T) {
	b := newSSEBroker()
	w := &gatedWriter{ResponseRecorder: httptest.NewRecorder(), gate: make(chan struct{}), first: make(chan struct{})}
	ended := make(chan struct{})
	go func() {
		b.ServeSSE(w, httptest.NewRequest("GET", "/stream", nil))
		close(ended)
	}()
	for { // wait until the handler is subscribed
		b.mu.Lock()
		n := len(b.clients)
		b.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	b.publish([]byte(`{"t":"out","d":"first"}`)) // the handler blocks writing this one
	<-w.first
	for i := 0; i < 100; i++ { // overflows the client's channel
		b.publish([]byte(`{"t":"out","d":"x"}`))
	}
	broadcastDone(b, nil)
	close(w.gate)

	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream stayed open")
	}
	if !strings.HasSuffix(w.Body.String(), "{\"ok\":true,\"t\":\"done\"}\n\n") {
		t.Fatalf("the final event is missing: %q", w.Body.String())
	}
}
