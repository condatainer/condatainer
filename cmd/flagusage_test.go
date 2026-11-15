package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var flagLineRE = regexp.MustCompile(`^\s+(-\w, )?--\w`)

func TestHelpWrapsFlagDescriptionsToTheWidth(t *testing.T) {
	defer func(w func() int) { helpWidth = w }(helpWidth)
	helpWidth = func() int { return 80 }

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, line := range strings.Split(c.UsageString(), "\n") {
			if len([]rune(line)) > 80 && flagLineRE.MatchString(line) {
				t.Errorf("%s: flag line is %d wide: %q", c.CommandPath(), len([]rune(line)), line)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

func TestHelpIsNotWrappedWithoutATerminal(t *testing.T) {
	if got := helpWidth(); got != 0 {
		t.Skipf("tests run on a terminal of width %d", got)
	}
	if !strings.Contains(createCmd.UsageString(), "Path to definition file (.yaml, .txt, .sh, .def, .sif, or a sandbox dir)") {
		t.Error("a flag description is on one line when help is not written to a terminal")
	}
}
