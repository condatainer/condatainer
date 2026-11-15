package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"

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
