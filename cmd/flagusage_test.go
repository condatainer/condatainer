package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

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

// helpLineMax is the widest a help line is, except a command line in Usage or
// Example, and a flag line, which wraps on a terminal.
const helpLineMax = 80

func TestHelpLinesFitTheTerminal(t *testing.T) {
	defer func(w func() int) { helpWidth = w }(helpWidth)
	helpWidth = func() int { return 0 }

	headingRE := regexp.MustCompile(`^[A-Za-z][A-Za-z ]*:$`)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		_ = c.Help()
		c.SetOut(nil)
		c.SetErr(nil)
		section := ""
		for _, line := range strings.Split(out.String(), "\n") {
			if headingRE.MatchString(line) {
				section = line
			}
			if utf8.RuneCountInString(line) <= helpLineMax || section == "Usage:" || strings.HasSuffix(section, "Flags:") {
				continue
			}
			if section == "Examples:" && !strings.Contains(line, " # ") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			t.Errorf("%s: help line is %d wide: %q", c.CommandPath(), utf8.RuneCountInString(line), line)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}
