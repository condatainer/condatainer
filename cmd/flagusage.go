package cmd

import (
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/condatainer/condatainer/internal/utils"
)

// writerWidth is the width of w when it is a terminal, and 0 when it is not one.
func writerWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return 0
}

// helpWidth is the width help text wraps to: that of stdout when it is a terminal, else of stderr,
// else 0 for no wrapping. Help written to a pipe or a file is never wrapped.
var helpWidth = func() int {
	if width := writerWidth(os.Stdout); width > 0 {
		return width
	}
	return writerWidth(os.Stderr)
}

// wrappedFlagUsages is the help of a FlagSet, with a long description wrapped under its own column.
func wrappedFlagUsages(fs *pflag.FlagSet) string {
	return fs.FlagUsagesWrapped(helpWidth())
}

// The usage template every command without its own usage function shares prints its flags wrapped.
func init() {
	cobra.AddTemplateFunc("wrappedFlagUsages", wrappedFlagUsages)
	tmpl := rootCmd.UsageTemplate()
	tmpl = strings.ReplaceAll(tmpl, ".LocalFlags.FlagUsages", "wrappedFlagUsages .LocalFlags")
	tmpl = strings.ReplaceAll(tmpl, ".InheritedFlags.FlagUsages", "wrappedFlagUsages .InheritedFlags")
	tmpl = strings.ReplaceAll(tmpl, "for more information about a command.", "for details.")
	rootCmd.SetUsageTemplate(tmpl)
}

// flagUsages renders a FlagSet's help like pflag's FlagUsages, but where no flag has
// a shorthand it drops the 4 columns pflag reserves for "-x, ", giving a 2-space
// indent. Sections with shorthands are returned unchanged, so "-n, --name" alignment holds.
func flagUsages(fs *pflag.FlagSet) string {
	hasShorthand := false
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Shorthand != "" && f.ShorthandDeprecated == "" {
			hasShorthand = true
		}
	})

	usage := wrappedFlagUsages(fs)
	if hasShorthand {
		return usage
	}

	// Long-only section: every line is indented by pflag's fixed 6 spaces
	// (2 base + 4 reserved shorthand columns). Drop 4 to land at a 2-space indent.
	// Shifting every line equally preserves the description alignment.
	lines := strings.Split(usage, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimPrefix(ln, "    ")
	}
	return strings.Join(lines, "\n")
}

// jsonFlag registers --json hidden: its output format is not final, so --help leaves it out.
func jsonFlag(fs *pflag.FlagSet, into *bool) {
	fs.BoolVar(into, "json", false, "Print JSON")
	_ = fs.MarkHidden("json")
}

// previewOnly reports whether a command that asks before it acts should only
// report. --json never prompts, so an unanswered prompt is a no: the command
// prints what it would do, and -y makes it act.
func previewOnly(asJSON bool) bool {
	return asJSON && !utils.ShouldAnswerYes()
}
