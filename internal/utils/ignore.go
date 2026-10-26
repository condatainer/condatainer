package utils

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// IgnoreMatch is what a list of ignore patterns says about one path.
type IgnoreMatch int

const (
	// IgnoreNone means no pattern matches the path.
	IgnoreNone IgnoreMatch = iota
	// IgnoreYes means the last matching pattern ignores the path.
	IgnoreYes
	// IgnoreNo means the last matching pattern is a `!` pattern, which includes the path.
	IgnoreNo
)

// Ignore is an ordered list of ignore patterns in the `.gitignore` syntax.
type Ignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	line    int
	negate  bool
	dirOnly bool
	re      *regexp.Regexp
}

// ParseIgnore reads the patterns of one ignore file.
//   - A blank line, a `#` comment and a pattern that cannot be compiled are skipped.
//   - Trailing spaces are dropped unless escaped with a backslash.
func ParseIgnore(text []byte) *Ignore {
	ig := &Ignore{}
	for i, line := range bytes.Split(text, []byte("\n")) {
		if rule, ok := parseIgnoreLine(strings.TrimSuffix(string(line), "\r")); ok {
			rule.line = i + 1
			ig.rules = append(ig.rules, rule)
		}
	}
	return ig
}

// Match answers for one slash-separated path relative to the directory the patterns belong to.
//   - The last matching pattern wins.
//   - Only the path itself is tested. A caller that walks a tree skips an ignored directory, so nothing inside it is asked about.
func (ig *Ignore) Match(rel string, isDir bool) IgnoreMatch {
	result := IgnoreNone
	for _, rule := range ig.rules {
		if rule.dirOnly && !isDir {
			continue
		}
		if rule.re.MatchString(rel) {
			result = IgnoreYes
			if rule.negate {
				result = IgnoreNo
			}
		}
	}
	return result
}

// Negations lists the 1-based lines that hold a `!` pattern.
func (ig *Ignore) Negations() []int {
	var lines []int
	for _, rule := range ig.rules {
		if rule.negate {
			lines = append(lines, rule.line)
		}
	}
	return lines
}

func parseIgnoreLine(line string) (ignoreRule, bool) {
	line = trimIgnoreSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	var rule ignoreRule
	if strings.HasPrefix(line, "!") {
		rule.negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return ignoreRule{}, false
	}
	body, ok := globToRegexp(line)
	if !ok {
		return ignoreRule{}, false
	}
	prefix := "^"
	if !anchored {
		prefix = "^(?:.*/)?"
	}
	re, err := regexp.Compile("(?s)" + prefix + body + "$")
	if err != nil {
		return ignoreRule{}, false
	}
	rule.re = re
	return rule, true
}

// trimIgnoreSpace drops trailing spaces that no backslash escapes.
func trimIgnoreSpace(line string) string {
	end := len(line)
	for end > 0 && line[end-1] == ' ' {
		if end >= 2 && line[end-2] == '\\' {
			break
		}
		end--
	}
	return line[:end]
}

// globToRegexp translates one pattern body into a regular expression.
//   - `*` and `?` stay inside one path component, and `[...]` is a character class.
//   - `**` crosses components as a whole component: leading `**/`, inner `/**/` and trailing `/**`. Anywhere else it reads as `*`.
//
// It reports false for a class whose range runs backwards.
func globToRegexp(glob string) (string, bool) {
	var out strings.Builder
	runes := []rune(glob)
	for i := 0; i < len(runes); i++ {
		switch c := runes[i]; c {
		case '\\':
			if i+1 < len(runes) {
				i++
				out.WriteString(regexp.QuoteMeta(string(runes[i])))
			} else {
				out.WriteString(`\\`)
			}
		case '?':
			out.WriteString(`[^/]`)
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				start := i == 0 || runes[i-1] == '/'
				j := i
				for j < len(runes) && runes[j] == '*' {
					j++
				}
				switch {
				case start && j == len(runes) && i > 0:
					out.WriteString(`.+`)
					i = j - 1
					continue
				case start && j < len(runes) && runes[j] == '/':
					out.WriteString(`(?:.*/)?`)
					i = j
					continue
				}
			}
			out.WriteString(`[^/]*`)
		case '[':
			class, next, ok, valid := globClass(runes, i)
			if !valid {
				return "", false
			}
			if !ok {
				out.WriteString(`\[`)
				continue
			}
			out.WriteString(class)
			i = next
		default:
			out.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return out.String(), true
}

// globClass translates the bracket expression that opens at runes[open].
//   - next is the index of its closing `]`.
//   - ok is false when no `]` closes it, so the `[` is a plain character.
//   - valid is false for a range that runs backwards.
func globClass(runes []rune, open int) (class string, next int, ok, valid bool) {
	i := open + 1
	negate := false
	if i < len(runes) && (runes[i] == '!' || runes[i] == '^') {
		negate = true
		i++
	}
	var items strings.Builder
	first := true
	for ; i < len(runes); i++ {
		c := runes[i]
		if c == ']' && !first {
			set := items.String()
			if set == "" {
				return "", 0, false, true
			}
			if negate {
				return "[^/" + set + "]", i, true, true
			}
			return "[" + set + "]", i, true, true
		}
		first = false
		if c == '[' && i+1 < len(runes) && runes[i+1] == ':' {
			if end := strings.Index(string(runes[i+2:]), ":]"); end >= 0 {
				name := string(runes[i+2:])[:end]
				items.WriteString("[:")
				items.WriteString(name)
				items.WriteString(":]")
				i += 2 + len([]rune(name)) + 1
				continue
			}
		}
		if c == '\\' && i+1 < len(runes) {
			i++
			c = runes[i]
		}
		hi := c
		if i+2 < len(runes) && runes[i+1] == '-' && runes[i+2] != ']' {
			i += 2
			hi = runes[i]
			if hi == '\\' && i+1 < len(runes) {
				i++
				hi = runes[i]
			}
		}
		if hi < c {
			return "", 0, false, false
		}
		if hi == c {
			fmt.Fprintf(&items, `\x{%x}`, c)
		} else {
			fmt.Fprintf(&items, `\x{%x}-\x{%x}`, c, hi)
		}
	}
	return "", 0, false, true
}
