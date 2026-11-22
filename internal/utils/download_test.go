package utils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A transfer that takes longer than the idle time in total still finishes while data keeps arriving.
func TestDownloadFileHasNoTotalTimeLimit(t *testing.T) {
	old := downloadIdle
	downloadIdle = 300 * time.Millisecond
	t.Cleanup(func() { downloadIdle = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 8 {
			w.Write([]byte("0123456789")) //nolint:errcheck
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "out")
	if err := DownloadFile(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if data, _ := os.ReadFile(dest); len(data) != 80 {
		t.Errorf("downloaded %d bytes, want 80", len(data))
	}
}

// A transfer that stops sending fails, names the stall and leaves no file.
func TestDownloadFileFailsWhenTheServerStalls(t *testing.T) {
	old := downloadIdle
	downloadIdle = 200 * time.Millisecond
	t.Cleanup(func() { downloadIdle = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("partial")) //nolint:errcheck
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "out")
	err := DownloadFile(context.Background(), srv.URL, dest)
	if err == nil || !strings.Contains(err.Error(), "no data received") {
		t.Fatalf("DownloadFile = %v, want a stall error", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("a partial file was left at %s", dest)
	}
}

// A cancelled context is reported as the caller's cancellation, not a stall.
func TestDownloadFileReportsCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := DownloadFile(ctx, srv.URL, filepath.Join(t.TempDir(), "out"))
	if err == nil || strings.Contains(err.Error(), "no data received") {
		t.Errorf("DownloadFile = %v, want a plain cancellation", err)
	}
}
