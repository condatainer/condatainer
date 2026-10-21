package cmd

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/spf13/cobra"
)

func TestWaitForPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	if err := waitForPort(context.Background(), port, 2*time.Second, 0); err != nil {
		t.Fatalf("listening port: %v", err)
	}

	ln.Close()
	if err := waitForPort(context.Background(), port, 400*time.Millisecond, 0); !errors.Is(err, errPortTimeout) {
		t.Fatalf("closed port: err = %v, want errPortTimeout", err)
	}
}

func TestWaitForPortStopsWhenServerExits(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	proc := exec.Command("sleep", "30")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })

	start := time.Now()
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}()
	err = waitForPort(context.Background(), port, 10*time.Second, proc.Process.Pid)
	if !errors.Is(err, errServerExited) {
		t.Fatalf("err = %v, want errServerExited", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("waited %s after the server exited", time.Since(start))
	}
}

func TestServerControlRefusedInsideContainer(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "1")
	for _, c := range []*cobra.Command{serverStartCmd, serverStopCmd, serverRestartCmd} {
		err := requireLoginNode(c)
		if err == nil || !strings.Contains(err.Error(), "cannot run inside a container") {
			t.Errorf("server %s inside a container: err = %v", c.Name(), err)
		}
	}
}

func TestServerStartedRecordsTheNodeAndJob(t *testing.T) {
	t.Setenv("CNT_HELPER_ID", "t-1")
	t.Setenv("CNT_HELPER_STATE_DIR", t.TempDir())
	t.Setenv("CNT_HELPER_JOB_ID", "42")
	serverStartedNode = "n1.example"
	t.Cleanup(func() { serverStartedNode = "" })
	if err := serverStartedCmd.RunE(serverStartedCmd, nil); err != nil {
		t.Fatal(err)
	}
	events, _, err := helper.ReadEventsFrom("t-1", 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, err = %v", events, err)
	}
	if ev := events[0]; ev.Type != "start" || ev.Node != "n1.example" || ev.JobID != "42" {
		t.Errorf("event = %+v, want a start on n1.example for job 42", ev)
	}
}
