package cmd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/condatainer/condatainer/internal/runtime/hostrun"
	"github.com/spf13/cobra"
)

func TestSchedulerGroupHasItsCommands(t *testing.T) {
	if schedulerCmd.Runnable() {
		t.Error("the bare group must print help, so it has no Run")
	}
	got := map[string]bool{}
	for _, c := range schedulerCmd.Commands() {
		got[c.Name()] = true
	}
	for _, name := range []string{"info", "partitions", "jobs", "cancel"} {
		if !got[name] {
			t.Errorf("scheduler has no %q command", name)
		}
	}
}

func TestSchedulerCommandsDoNotPrintUsageForARuntimeError(t *testing.T) {
	for _, c := range schedulerCmd.Commands() {
		if !c.SilenceUsage {
			t.Errorf("scheduler %s prints its usage after a runtime error", c.Name())
		}
	}
}

// Help is read in a terminal about 100 columns wide. A longer line wraps and breaks its bullet.
func TestSchedulerHelpLinesFitTheTerminal(t *testing.T) {
	for _, c := range append([]*cobra.Command{schedulerCmd}, schedulerCmd.Commands()...) {
		text := c.Short + "\n" + c.Long + "\n" + c.Example + "\n" + c.UsageString()
		for _, line := range strings.Split(text, "\n") {
			if n := utf8.RuneCountInString(line); n >= 100 {
				t.Errorf("scheduler help of %q has a line of %d columns: %.60s", c.Name(), n, line)
			}
		}
	}
}

// Every member takes --host and --host-on.
func TestSchedulerCommandsTakeTheHostFlags(t *testing.T) {
	for _, c := range schedulerCmd.Commands() {
		for _, name := range []string{"host", "host-on"} {
			if c.Flags().Lookup(name) == nil {
				t.Errorf("scheduler %s has no --%s", c.Name(), name)
			}
		}
	}
}

// hostFlagsCommand is a scheduler member with the host flags, parsed from args, and the host's call.
func hostFlagsCommand(t *testing.T, container bool, args ...string) (*cobra.Command, *hostrun.Call) {
	t.Helper()
	oldHost, oldOn, oldInside, oldCall := schedulerHost, schedulerHostOn, runInsideContainer, hostRunCall
	schedulerHost, schedulerHostOn = false, ""
	runInsideContainer = func() bool { return container }
	sent := &hostrun.Call{}
	hostRunCall = func(_ context.Context, call hostrun.Call) (int, error) { *sent = call; return 0, nil }
	t.Cleanup(func() {
		schedulerHost, schedulerHostOn, runInsideContainer, hostRunCall = oldHost, oldOn, oldInside, oldCall
	})
	t.Setenv(hostrun.EnvSock, "/.condatainer/host/sock")

	c := &cobra.Command{Use: "jobs"}
	c.Flags().Bool("tasks", false, "")
	addHostFlags(c.Flags())
	if err := c.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return c, sent
}

// Inside a container, --host sends the same command to the host, with --host-on as the node.
func TestRunOnHostIfAskedSendsTheCommand(t *testing.T) {
	c, sent := hostFlagsCommand(t, true, "--host", "--host-on=login", "--tasks", "--", "-x", "245068")
	done, err := runOnHostIfAsked(c, c.Flags().Args())
	if err != nil || !done {
		t.Fatalf("done %v, err %v", done, err)
	}
	want := []string{"condatainer", "scheduler", "jobs", "--tasks=true", "--", "-x", "245068"}
	if !slices.Equal(sent.Argv, want) {
		t.Errorf("sent %q, want %q", sent.Argv, want)
	}
	if sent.On != "login" || sent.Sock != "/.condatainer/host/sock" {
		t.Errorf("on %q, socket %q", sent.On, sent.Sock)
	}
}

// On the host --host changes nothing, and nothing is sent.
func TestRunOnHostIfAskedOnTheHostRunsLocally(t *testing.T) {
	c, sent := hostFlagsCommand(t, false, "--host")
	if done, err := runOnHostIfAsked(c, nil); done || err != nil {
		t.Errorf("done %v, err %v, want a local run", done, err)
	}
	if sent.Argv != nil {
		t.Errorf("sent %q from the host", sent.Argv)
	}
}

// Without --host a container runs the command itself, and --host-on alone is refused.
func TestRunOnHostIfAskedNeedsHostForHostOn(t *testing.T) {
	c, sent := hostFlagsCommand(t, true)
	if done, err := runOnHostIfAsked(c, nil); done || err != nil || sent.Argv != nil {
		t.Errorf("done %v, err %v, sent %q, want a local run", done, err, sent.Argv)
	}
	for _, container := range []bool{true, false} {
		c, _ := hostFlagsCommand(t, container, "--host-on=login")
		if _, err := runOnHostIfAsked(c, nil); !errors.Is(err, errHostOnNeedsHost) {
			t.Errorf("in a container %v: err = %v, want errHostOnNeedsHost", container, err)
		}
	}
}

// A container with no host-run says so, naming host-run.
func TestRunOnHostIfAskedWithNoRelayIsAnError(t *testing.T) {
	c, _ := hostFlagsCommand(t, true, "--host")
	hostRunCall = func(context.Context, hostrun.Call) (int, error) { return 0, hostrun.ErrNoRelay }
	_, err := runOnHostIfAsked(c, nil)
	if !errors.Is(err, hostrun.ErrNoRelay) || !strings.Contains(err.Error(), "host-run") {
		t.Errorf("err = %v, want one that wraps ErrNoRelay and names host-run", err)
	}
}

// A container with a relay to the host is told to add --host. Anywhere else the message is the plain one.
func TestNoSchedulerHintsHostOnlyWithARelay(t *testing.T) {
	old := runInsideContainer
	t.Cleanup(func() { runInsideContainer = old })
	cases := []struct {
		container bool
		sock      string
		hint      bool
	}{
		{true, "/.condatainer/host/sock", true},
		{true, "", false},
		{false, "/.condatainer/host/sock", false},
	}
	for _, c := range cases {
		runInsideContainer = func() bool { return c.container }
		t.Setenv(hostrun.EnvSock, c.sock)
		err := noScheduler()
		if !errors.Is(err, errNoScheduler) {
			t.Errorf("container %v, socket %q: err = %v, want errNoScheduler", c.container, c.sock, err)
		}
		if got := strings.Contains(err.Error(), "--host"); got != c.hint {
			t.Errorf("container %v, socket %q: hint %v, want %v", c.container, c.sock, got, c.hint)
		}
	}
}
