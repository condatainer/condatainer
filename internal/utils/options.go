package utils

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrHeaderOption is returned for an option a header does not declare, a repeated option, or a value of the wrong kind.
var ErrHeaderOption = errors.New("invalid header option")

type optionKind int

const (
	optText optionKind = iota
	optBool
)

// headerOptions declares the options each header takes. A header that is absent takes none, and its value is read as written.
//   - `=` is common in values (`#ENV: NAME=x`, `#PARAM: KEY=default`, a URL query), so only a header listed here reads options.
var headerOptions = map[string]map[string]optionKind{
	"#DEP": {"optional": optBool},
}

// Options holds the options written after a header's value.
type Options map[string]string

// Bool reports whether a boolean option is set to true.
func (o Options) Bool(name string) bool { return o[name] == "true" }

// SplitOptions separates the value of an annotation from its options.
//   - The first word is always the value. Options start at the first later word shaped `name=...`, a name being lower-case letters, digits and underscores.
//   - A word before the options that is not an option stays in the value, so `star >= 2.7` is one value.
//   - Every word after the first option must be an option, so a stray word never turns an option into part of the value.
//   - A boolean is `true` or `false`. Quotes are not special.
//   - A header with no declared options returns its value unchanged.
//   - An undeclared option, a repeated one, an empty or non-boolean value and a word after an option are refused, naming the key and the line.
func SplitOptions(a Annotation) (string, Options, error) {
	declared, ok := headerOptions[a.Key]
	if !ok {
		return a.Value, nil, nil
	}
	words := optionWords(a.Value)
	first := 1
	for first < len(words) && !isOptionWord(words[first].text) {
		first++
	}
	if first >= len(words) {
		return a.Value, nil, nil
	}
	head := strings.TrimSpace(a.Value[:words[first].start])
	opts := Options{}
	for _, w := range words[first:] {
		if !isOptionWord(w.text) {
			return "", nil, fmt.Errorf("%w: %s on line %d: %q comes after an option; options go last",
				ErrHeaderOption, a.Key, a.Line, w.text)
		}
		name, value, _ := strings.Cut(w.text, "=")
		kind, known := declared[name]
		if !known {
			return "", nil, fmt.Errorf("%w: %s on line %d: %q is not an option; valid options are %s",
				ErrHeaderOption, a.Key, a.Line, name, optionNames(declared))
		}
		if _, dup := opts[name]; dup {
			return "", nil, fmt.Errorf("%w: %s on line %d: %q is written twice", ErrHeaderOption, a.Key, a.Line, name)
		}
		if value == "" {
			return "", nil, fmt.Errorf("%w: %s on line %d: %q needs a value", ErrHeaderOption, a.Key, a.Line, name)
		}
		if kind == optBool && value != "true" && value != "false" {
			return "", nil, fmt.Errorf("%w: %s on line %d: %s=%s must be true or false",
				ErrHeaderOption, a.Key, a.Line, name, value)
		}
		opts[name] = value
	}
	return head, opts, nil
}

type optionWord struct {
	text  string
	start int
}

// optionWords splits s at blanks, recording where each word starts.
func optionWords(s string) []optionWord {
	var words []optionWord
	for i := 0; i < len(s); {
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		words = append(words, optionWord{text: s[start:i], start: start})
	}
	return words
}

// isOptionWord reports whether w is `name=...` with a lower-case name.
func isOptionWord(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func optionNames(declared map[string]optionKind) string {
	names := make([]string, 0, len(declared))
	for n := range declared {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
