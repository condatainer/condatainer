package server

import (
	"net/http"
	"sync/atomic"
	"time"
)

const (
	// eventsCoalesce is how long a change waits for others before one event goes out.
	eventsCoalesce = 100 * time.Millisecond
	// eventsKeepalive is the gap between comment lines that keep a quiet stream open through proxies.
	eventsKeepalive = 25 * time.Second
)

var (
	// jobEvents carries the "jobs changed" events of every connected dashboard.
	jobEvents = newSSEBroker()
	// jobsNotifyPending is set while an event is waiting out eventsCoalesce.
	jobsNotifyPending atomic.Bool
	jobsEventMsg      = []byte(`{"t":"jobs"}`)
)

// notifyJobsChanged tells every connected dashboard that a job, its status or
// its last message changed. Changes within eventsCoalesce share one event.
func notifyJobsChanged() {
	if jobsNotifyPending.Swap(true) {
		return
	}
	time.AfterFunc(eventsCoalesce, func() {
		jobsNotifyPending.Store(false)
		jobEvents.publish(jobsEventMsg)
	})
}

// handleEvents serves GET /api/events as SSE.
//   - A client gets {"t":"jobs"} when it connects and again after each change to the jobs. The event carries no data: the client refetches the lists.
//   - The stream stays open until the client leaves. A comment line every eventsKeepalive keeps it alive.
//   - An event is dropped for a client that is not reading, so the dashboard also refetches on its own timer.
func (s *srv) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, _, _ := jobEvents.subscribe()
	defer jobEvents.unsubscribe(ch)
	w.Write(append(append([]byte("data: "), jobsEventMsg...), '\n', '\n')) //nolint:errcheck
	flusher.Flush()

	ticker := time.NewTicker(eventsKeepalive)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			w.Write(append(append([]byte("data: "), data...), '\n', '\n')) //nolint:errcheck
			flusher.Flush()
		case <-ticker.C:
			w.Write([]byte(": keepalive\n\n")) //nolint:errcheck
			flusher.Flush()
		}
	}
}
