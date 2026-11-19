package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as the relay command.
func TestMain(m *testing.M) {
	if os.Getenv("CNT_TEST_EXEC_RELAY") == "1" {
		if RunExecRelay(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }() //nolint:errcheck
		}
	}()
	return ln.Addr().String()
}

func TestDialViaExec(t *testing.T) {
	addr := echoServer(t)
	dial, stop, done, err := DialViaExec([]string{"env", "CNT_TEST_EXEC_RELAY=1", os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Binary frames with no newline echo back promptly, request after request.
	c, err := dial(ctx, "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		msg := make([]byte, 20)
		rand.Read(msg)                                 //nolint:errcheck
		c.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
		if _, err := c.Write(msg); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("round trip %d: err=%v equal=%v", i, err, bytes.Equal(got, msg))
		}
	}
	c.Close()

	// Concurrent bulk transfers share the one relay.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := dial(ctx, "tcp", addr)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			data := make([]byte, 200000)
			rand.Read(data)  //nolint:errcheck
			go c.Write(data) //nolint:errcheck
			got := make([]byte, len(data))
			c.SetDeadline(time.Now().Add(15 * time.Second)) //nolint:errcheck
			if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, data) {
				t.Errorf("bulk echo: err=%v equal=%v", err, bytes.Equal(got, data))
			}
		}()
	}
	wg.Wait()

	// A port nothing listens on is refused, not hung.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().String()
	ln.Close()
	if _, err := dial(ctx, "tcp", closed); err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
	if _, err := dial(ctx, "tcp", "10.0.0.1:80"); err == nil {
		t.Fatal("dial to a non-loopback host succeeded")
	}

	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done not closed after stop")
	}
	if _, err := dial(ctx, "tcp", addr); err == nil {
		t.Fatal("dial after stop succeeded")
	}
}

// A relay that cannot start is reported with what the command printed.
func TestDialViaExecReportsRelayFailure(t *testing.T) {
	_, _, _, err := DialViaExec([]string{"sh", "-c", "echo boom >&2; exit 3"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the command's message", err)
	}
}

// Data frames carry any bytes, including newlines and NULs; a stray line surfaces as an unhandled op.
func TestExecFrameRoundTrip(t *testing.T) {
	payload := []byte("a\nb\x00\r\n\xff\n")
	var buf bytes.Buffer
	buf.WriteString("warning: not a frame\n")
	for _, f := range []frame{{op: "O", id: "1", arg: "8080"}, {op: "D", id: "1", data: payload}, {op: "C", id: "1"}} {
		if err := writeFrame(&buf, f); err != nil {
			t.Fatal(err)
		}
	}
	r := bufio.NewReader(&buf)
	f, _ := readFrame(r)
	if f.op != "warning:" { // a non-frame line parses as an unknown op and is ignored by callers
		t.Fatalf("first = %+v", f)
	}
	for _, want := range []frame{{op: "O", id: "1", arg: "8080"}, {op: "D", id: "1", arg: "8", data: payload}, {op: "C", id: "1"}} {
		got, err := readFrame(r)
		if err != nil || got.op != want.op || got.id != want.id || got.arg != want.arg || !bytes.Equal(got.data, want.data) {
			t.Fatalf("got %+v, %v; want %+v", got, err, want)
		}
	}
}

// setExecLimits shortens the liveness limits for one test.
func setExecLimits(t *testing.T, ping, dead, ack time.Duration) {
	t.Helper()
	p, d, a := execPingInterval, execDeadAfter, execAckTimeout
	execPingInterval, execDeadAfter, execAckTimeout = ping, dead, ack
	t.Cleanup(func() { execPingInterval, execDeadAfter, execAckTimeout = p, d, a })
}

// A relay that answers pings stays open while it carries no traffic.
func TestExecPingKeepsIdleRelayAlive(t *testing.T) {
	setExecLimits(t, 20*time.Millisecond, 200*time.Millisecond, time.Minute)
	_, stop, done, err := DialViaExec([]string{"env", "CNT_TEST_EXEC_RELAY=1", os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-done:
		t.Fatal("an idle relay that answers pings was closed")
	case <-time.After(800 * time.Millisecond):
	}
}

// A relay that stops sending frames is killed, and done closes.
func TestExecSilentRelayIsKilled(t *testing.T) {
	setExecLimits(t, 20*time.Millisecond, 100*time.Millisecond, time.Minute)
	_, stop, done, err := DialViaExec([]string{"sh", "-c", "echo R; sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a silent relay was not closed")
	}
}

// A dial the node never answers fails with a backend error, not a context error, and closes the relay.
func TestExecDialAckTimeout(t *testing.T) {
	setExecLimits(t, time.Hour, time.Hour, 100*time.Millisecond)
	dial, stop, done, err := DialViaExec([]string{"sh", "-c", "echo R; cat >/dev/null"})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = dial(ctx, "tcp", "127.0.0.1:9")
	if !errors.Is(err, errExecAckTimeout) {
		t.Fatalf("err = %v, want the ack timeout", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v is a context error, which the proxy takes for a client that left", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay still open after a dial timed out")
	}
}
