// Package subject reads the condition a header puts on a slot, and matches it against the overlay bound to the slot.
package subject

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// ErrInvalid is returned for a condition value that cannot be read.
var ErrInvalid = errors.New("invalid condition")

// regexPrefix marks a value that is one regular expression.
const regexPrefix = "regex:"

// Subject is what a conditional header says a slot's overlay must be: one of a list of names, or one regular expression.
type Subject struct {
	values []string
	re     *regexp.Regexp
}

// Parse reads the value of a condition such as `grch38,grcm39` or `regex:^grch38/`.
//   - A plain value is a comma list. Each entry names an overlay, or the start of one by whole parts.
//   - A value that starts with regex: is one regular expression, never split at commas. It is used as written, so it matches anywhere in the name unless it holds `^` or `$`.
//   - A name that is empty, a regular expression that does not compile and an empty list entry are errors.
func Parse(value string) (Subject, error) {
	if pattern, ok := strings.CutPrefix(value, regexPrefix); ok {
		if pattern == "" {
			return Subject{}, fmt.Errorf("%w: %s needs a pattern", ErrInvalid, regexPrefix)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return Subject{}, fmt.Errorf("%w: %s%s: %v", ErrInvalid, regexPrefix, pattern, err)
		}
		return Subject{values: []string{value}, re: re}, nil
	}
	values, err := splitList(value)
	if err != nil {
		return Subject{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for i, v := range values {
		values[i] = normalize(v)
	}
	return Subject{values: values}, nil
}

// Match reports whether the overlay name satisfies the subject, and which entry did.
//   - A plain entry matches the name itself, or a name that continues it after a `/` or a `.`.
//     `gencode/4` matches `gencode/4.1` and `gencode/4/x`, and not `gencode/47`.
//   - A file is matched by its path.
func (s Subject) Match(name string) (string, bool) {
	if s.re != nil {
		if s.re.MatchString(name) {
			return s.values[0], true
		}
		return "", false
	}
	name = normalize(name)
	for _, v := range s.values {
		if name == v || strings.HasPrefix(name, v+"/") || strings.HasPrefix(name, v+".") {
			return v, true
		}
	}
	return "", false
}

// String is the value as written.
func (s Subject) String() string {
	if s.re != nil {
		return s.values[0]
	}
	return strings.Join(s.values, ",")
}

// splitList splits a comma list and refuses an empty entry.
func splitList(s string) ([]string, error) {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item == "" {
			return nil, errors.New("an entry is empty")
		}
		out = append(out, item)
	}
	return out, nil
}

func normalize(v string) string {
	if utils.IsPathDep(v) {
		return v
	}
	return utils.NormalizeModule(v)
}
