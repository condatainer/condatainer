package cmd

import (
	"strings"

	"github.com/spf13/pflag"

	"github.com/condatainer/condatainer/internal/utils"
)

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

	usage := fs.FlagUsages()
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
