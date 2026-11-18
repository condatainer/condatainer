package utils

import (
	"errors"
	"fmt"
	"slices"
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

// OptionOptional makes a #DEP: line optional. Only a reader that names it accepts it.
const OptionOptional = "optional"

// headerOptions declares the options a header can carry, and their kinds. A header that is absent takes none, and its value is read as written.
//   - `=` is common in values (`#ENV: NAME=x`, `#PARAM: KEY=default`, a URL query), so only a header listed here reads options.
var headerOptions = map[string]map[string]optionKind{
	"#DEP": {OptionOptional: optBool},
}

// Options holds the options written after a header's value.
type Options map[string]string

// Bool reports whether a boolean option is set to true.
func (o Options) Bool(name string) bool { return o[name] == "true" }

// SplitOptions separates the value of an annotation from its options. The reader names the options it accepts.
//   - The first word is always the value. Options start at the first later word shaped `name=...`, a name being lower-case letters, digits and underscores.
//   - A word before the options that is not an option stays in the value, so `star >= 2.7` is one value.
//   - Every word after the first option must be an option, so a stray word never turns an option into part of the value.
//   - A boolean is `true` or `false`, and an option name with no value is refused. Quotes are not special.
//   - A header with no declared options returns its value unchanged.
//   - An option the reader does not accept is unknown, even when another reader accepts it.
//   - An unknown option, a repeated one, an empty or non-boolean value and a word after an option are refused, naming the key and the line.
func SplitOptions(a Annotation, accepts ...string) (string, Options, error) {
	declared, ok := headerOptions[a.Key]
	if !ok {
		return a.Value, nil, nil
	}
	words := optionWords(a.Value)
	first := 1
	for first < len(words) && !isOptionWord(words[first].text) {
		if slices.Contains(accepts, words[first].text) {
			return "", nil, fmt.Errorf("%w: %s on line %d: write %s=true; an option has a value",
				ErrHeaderOption, a.Key, a.Line, words[first].text)
		}
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
		if !slices.Contains(accepts, name) {
			return "", nil, fmt.Errorf("%w: %s on line %d: %q is not an option; %s",
				ErrHeaderOption, a.Key, a.Line, name, validOptions(accepts))
		}
		kind := declared[name]
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

func validOptions(accepts []string) string {
	if len(accepts) == 0 {
		return "this reader takes no options"
	}
	names := append([]string(nil), accepts...)
	sort.Strings(names)
	return "valid options are " + strings.Join(names, ", ")
}
