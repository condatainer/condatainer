package utils

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var phRange = regexp.MustCompile(`^(\d+)-(\d+)$`)

// ParseValueList expands a value list into concrete values, ranges included. The
// separator sets the order — comma sorts newest first, pipe keeps the order
// written, * is always last.
func ParseValueList(raw string) []string {
	raw = strings.TrimSpace(raw)
	sep, sorted := ",", true
	if strings.Contains(raw, "|") {
		sep, sorted = "|", false
	}

	var values []string
	open := false
	seen := map[string]bool{}
	for _, tok := range strings.Split(raw, sep) {
		tok = strings.TrimSpace(tok)
		switch {
		case tok == "":
			continue
		case tok == "*":
			open = true
			continue
		}
		for _, v := range expandRange(tok) {
			if !seen[v] {
				seen[v] = true
				values = append(values, v)
			}
		}
	}

	if sorted {
		slices.SortStableFunc(values, func(a, b string) int { return CompareVersions(b, a) })
	}
	if open {
		values = append(values, "*")
	}
	return values
}

// expandRange turns "22-49" into every integer between, or returns the token.
func expandRange(tok string) []string {
	m := phRange.FindStringSubmatch(tok)
	if m == nil {
		return []string{tok}
	}
	lo, err1 := strconv.Atoi(m[1])
	hi, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return []string{tok}
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	out := make([]string, 0, hi-lo+1)
	for v := lo; v <= hi; v++ {
		out = append(out, strconv.Itoa(v))
	}
	return out
}
