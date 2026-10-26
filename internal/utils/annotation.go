package utils

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Annotation is one `#KEY: value ## note` declaration.
//
// Every consumer of script metadata reads these — recipes, user scripts,
// project scanning — so a key is tokenized in exactly one place and means the
// same thing everywhere it appears.
type Annotation struct {
	// Key is the notation including its '#', e.g. "#DEP".
	Key string
	// Value is the text after the colon up to the first `##`, trimmed. A single `#` is part of it.
	Value string
	// Note is the text after `##`, unparsed. Empty when there is none.
	Note string
	// Line is the 1-based line the annotation was written on.
	Line int
}

// ScanAnnotations returns every annotation in the header block of text, in the order written.
//   - A line qualifies when, after leading blanks and tabs, it begins with '#', then a key of upper-case letters, digits or underscores, then ':'.
//   - Only the header block is read. A later line is not an annotation, whatever it looks like, so a heredoc that writes another script declares nothing.
//   - The upper-case key keeps prose out: `# note: rerun weekly` is a comment, `#DEP: star/2.7.11b` is an annotation.
func ScanAnnotations(text []byte) []Annotation {
	lines := strings.Split(string(text), "\n")
	return scanLines(lines, 0, HeaderEnd(lines))
}

// StrayAnnotations returns the annotations written after the header block, which no reader honors.
//   - With keys, only those are returned: a reader reports the keys it owns, so a comment that only looks like a header is not reported.
func StrayAnnotations(text []byte, keys ...string) []Annotation {
	lines := strings.Split(string(text), "\n")
	stray := scanLines(lines, HeaderEnd(lines), len(lines))
	if len(keys) == 0 {
		return stray
	}
	var out []Annotation
	for _, a := range stray {
		if slices.Contains(keys, a.Key) {
			out = append(out, a)
		}
	}
	return out
}

// HeaderEnd returns how many leading lines form the header block: blank lines and comment lines, up to the first line of code.
//   - The shebang is a comment, so it belongs to the block.
//   - Schedulers read their directives the same way, so a script means one thing whoever reads it.
func HeaderEnd(lines []string) int {
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line != "" && !strings.HasPrefix(line, "#") {
			return i
		}
	}
	return len(lines)
}

func scanLines(lines []string, from, to int) []Annotation {
	var out []Annotation
	for i := from; i < to; i++ {
		line := strings.TrimLeft(strings.TrimSuffix(lines[i], "\r"), " \t")
		if !strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := cutKey(line)
		if !ok {
			continue
		}
		value, note := splitNote(rest)
		out = append(out, Annotation{Key: key, Value: value, Note: note, Line: i + 1})
	}
	return out
}

var warnedStray sync.Map

// WarnStray prints one warning for each annotation in stray, once per file and line.
func WarnStray(path string, stray []Annotation) {
	for _, a := range stray {
		WarnIgnoredLine(path, a.Line, a.Key+":")
	}
}

// WarnIgnoredLine says a line after the header block is ignored, once per file and line.
func WarnIgnoredLine(path string, line int, what string) {
	if _, seen := warnedStray.LoadOrStore(fmt.Sprintf("%s:%d", path, line), true); seen {
		return
	}
	PrintWarning("%s line %d: %s is after the first command and is ignored; move it up into the header", path, line, what)
}

// Flag reads a flag header: it is on when the line is present, with no value or `true`.
//   - Any other value is an error naming the key and line, so `false` cannot read as on.
func Flag(annotations []Annotation, key string) (bool, error) {
	a := SelectAnnotations(annotations, key)
	if len(a) == 0 {
		return false, nil
	}
	if v := a[0].Value; v != "" && v != "true" {
		return false, fmt.Errorf("%s on line %d: %q is not allowed; a flag header takes no value", key, a[0].Line, v)
	}
	return true, nil
}

// FindAnnotation returns the value of the first annotation with key, and whether one was
// present. Most keys are single-valued, and the first wins.
func FindAnnotation(annotations []Annotation, key string) (string, bool) {
	for _, a := range annotations {
		if a.Key == key {
			return a.Value, true
		}
	}
	return "", false
}

// SelectAnnotations returns every annotation with key, in order.
func SelectAnnotations(annotations []Annotation, key string) []Annotation {
	var out []Annotation
	for _, a := range annotations {
		if a.Key == key {
			out = append(out, a)
		}
	}
	return out
}

// cutKey splits `#KEY:rest`, requiring an upper-case key immediately after the
// '#'. It reports false for a line that only looks like one, such as a shebang
// or a prose comment containing a colon.
func cutKey(line string) (key, rest string, ok bool) {
	body, rest, ok := strings.Cut(line[1:], ":")
	if !ok || body == "" {
		return "", "", false
	}
	for _, r := range body {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return "", "", false
		}
	}
	return "#" + body, rest, true
}

// splitNote separates an annotation value from its ## note.
func splitNote(s string) (value, note string) {
	if before, after, ok := strings.Cut(s, "##"); ok {
		return strings.TrimSpace(before), strings.TrimSpace(after)
	}
	return strings.TrimSpace(s), ""
}
