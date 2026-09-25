package cmd

import (
	"strings"

	"github.com/spf13/pflag"
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
