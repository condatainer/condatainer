package key

import (
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// hasComponents reports whether dep's slash-separated components occur as a contiguous
// run of name's. Components compare as exact strings, never semantically (2.7.11b and
// 2.7.11 name different builds), and never as substrings: star/2.7.11b must not match
// inside star2.7.11b-old. Role uses it: an app or os dependency
// contributes to equivalence only when this is true, otherwise it is build history.
func hasComponents(name, dep string) bool {
	n := strings.Split(utils.NormalizeModule(name), "/")
	d := strings.Split(utils.NormalizeModule(dep), "/")
	if len(d) == 0 || len(d) > len(n) {
		return false
	}
	for i := 0; i+len(d) <= len(n); i++ {
		if slices.Equal(n[i:i+len(d)], d) {
			return true
		}
	}
	return false
}
