package utils

import (
	"io"
	"os"
	"testing"
)

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestPrintLinkIsTheLinkAloneOnALine(t *testing.T) {
	const link = "http://localhost:1/?cnt_token=abcdefghijklmnop"
	if got := captureStderr(t, func() { PrintLink(link) }); got != link+"\n" {
		t.Errorf("PrintLink wrote %q, want the link and a newline only", got)
	}
}

func TestPrintLinkIsQuietInQuietMode(t *testing.T) {
	old := QuietMode
	QuietMode = true
	defer func() { QuietMode = old }()
	if got := captureStderr(t, func() { PrintLink("http://localhost:1/") }); got != "" {
		t.Errorf("PrintLink wrote %q in quiet mode", got)
	}
}
