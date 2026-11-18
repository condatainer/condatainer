package utils

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrEmptyDep is returned by ParseDep for a blank dependency.
var ErrEmptyDep = errors.New("empty dependency")

// Dep is one #DEP: edge. The preferred version is the implicit upper bound, so
// Min "2.7.0" with Version "2.7.11b" admits [2.7.0, 2.7.11b].
type Dep struct {
	Name    string // module name, e.g. "star" or "grch38/genome"
	Version string // preferred version; empty when the dep names none
	Min     string // from >= or >; empty when unconstrained
	Op      string // ">=" or ">"; empty when unconstrained
}

// String renders the dep back to its #DEP: form.
func (d Dep) String() string {
	return d.NameVersion() + d.Op + d.Min
}

// NameVersion is the module path without the constraint: what names an artifact,
// as against what a dep will accept.
func (d Dep) NameVersion() string {
	if d.Version == "" {
		return d.Name
	}
	return d.Name + "/" + d.Version
}

// NormalizeModule folds the spellings of one module into name/version form, converting = and -- to / and trimming space.
//   - Constraint suffixes survive.
//   - A trailing @<identity> survives: @ separates a name from an identity, never a version.
//   - Exported because it decides what a name is: index keys use this form, so a second implementation elsewhere would make one string resolve two ways.
func NormalizeModule(nameVersion string) string {
	nv, op, min := splitConstraint(strings.TrimSpace(nameVersion))
	nv = strings.ReplaceAll(nv, "--", "/")
	nv = strings.ReplaceAll(nv, "=", "/")
	return nv + op + min
}

// ParseDep parses a #DEP: value such as "star/2.7.11b>=2.7.0". The last path
// component is the version, so a template dep keeps its token there.
func ParseDep(raw string) (Dep, error) {
	nv, op, min := splitConstraint(NormalizeModule(raw))
	if nv == "" {
		return Dep{}, ErrEmptyDep
	}
	d := Dep{Name: nv, Op: op, Min: min}
	if i := strings.LastIndex(nv, "/"); i >= 0 {
		d.Name, d.Version = nv[:i], nv[i+1:]
	}
	return d, nil
}

// splitConstraint separates a spec from its >= or > suffix.
func splitConstraint(raw string) (nameVersion, op, min string) {
	for _, sep := range []string{">=", ">"} {
		if before, after, ok := strings.Cut(raw, sep); ok {
			return strings.TrimSpace(before), sep, strings.TrimSpace(after)
		}
	}
	return raw, "", ""
}

// Satisfies reports whether version falls in the range the dep admits: at or
// above Min per Op, never above Version. Unconstrained admits any version
// sharing Version as a dot-component prefix — "17" admits "17.0.18" the same
// family match Conda's own MatchSpec syntax gives a bare "=17".
func (d Dep) Satisfies(version string) bool {
	if d.Op == "" {
		return d.Version == "" || versionHasPrefix(version, d.Version)
	}
	switch cmp := CompareVersions(version, d.Min); d.Op {
	case ">=":
		if cmp < 0 {
			return false
		}
	case ">":
		if cmp <= 0 {
			return false
		}
	default:
		return false
	}
	return d.Version == "" || CompareVersions(version, d.Version) <= 0
}

// versionHasPrefix reports whether every dot-separated component of prefix
// equals the corresponding component of version, in order. A full version is
// trivially its own prefix, so this subsumes equality rather than replacing
// it — only a genuinely shorter spec ("17", "17.0") gains new matches.
func versionHasPrefix(version, prefix string) bool {
	vParts := strings.Split(version, ".")
	pParts := strings.Split(prefix, ".")
	if len(pParts) > len(vParts) {
		return false
	}
	for i, p := range pParts {
		if vParts[i] != p {
			return false
		}
	}
	return true
}

// IsPathDep reports whether a #DEP: value names an overlay file rather than a module name/version.
//   - It decides which grammar a dependency is read with: NormalizeModule and ParseDep apply to names only, and a second answer elsewhere would let one string be a name in one place and a path in another.
//   - The extensions are IsOverlay's plus .sif.
func IsPathDep(value string) bool {
	value = strings.TrimSpace(value)
	return IsOverlay(value) || IsSif(value)
}

// DepLine is one #DEP: line.
type DepLine struct {
	// Name is the module name, normalized, or an overlay path as written. A {KEY} token survives.
	Name string
	// Optional is set by `optional=true`.
	Optional bool
	// Line is the 1-based line the declaration was written on.
	Line int
}

// DependencyLines returns the #DEP: lines in the order written, deduplicated by name. The reader names the options it accepts.
//   - A name is normalized, and a path is kept as written. A {KEY} token survives, for a reader that fills it.
//   - A name written twice keeps its first place, and is required when any of its lines is.
//   - A line whose options are refused is left out, and its error is returned with the lines that did read.
func DependencyLines(annotations []Annotation, accepts ...string) ([]DepLine, error) {
	var lines []DepLine
	var problems []error
	index := map[string]int{}
	for _, a := range SelectAnnotations(annotations, "#DEP") {
		name, opts, err := SplitOptions(a, accepts...)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if name == "" {
			continue
		}
		if !IsPathDep(name) {
			name = NormalizeModule(name)
		}
		optional := opts.Bool(OptionOptional)
		if i, seen := index[name]; seen {
			lines[i].Optional = lines[i].Optional && optional
			continue
		}
		index[name] = len(lines)
		lines = append(lines, DepLine{Name: name, Optional: optional, Line: a.Line})
	}
	return lines, errors.Join(problems...)
}

// RequiredNames returns the names of the lines that are not optional, in order.
func RequiredNames(lines []DepLine) []string { return namesWhere(lines, false) }

// OptionalNames returns the names of the optional lines, in order.
func OptionalNames(lines []DepLine) []string { return namesWhere(lines, true) }

func namesWhere(lines []DepLine, optional bool) []string {
	names := []string{}
	for _, l := range lines {
		if l.Optional == optional {
			names = append(names, l.Name)
		}
	}
	return names
}

// ScriptDependencies returns the dependencies a script's #DEP: annotations declare, deduplicated.
//   - A name is normalized, and a path is kept as written.
//   - Only the header block is read. A #DEP: after the first command is ignored and warned about.
//   - Only #DEP: counts. A `module load` line names the site's module tree, not an artifact.
//   - An option on a #DEP: line is refused, since a script takes none.
func ScriptDependencies(scriptPath string) ([]string, error) {
	text, err := os.ReadFile(scriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("script not found at %s", scriptPath)
		}
		return nil, fmt.Errorf("failed to open script: %w", err)
	}

	WarnStray(scriptPath, StrayAnnotations(text, "#DEP"))
	lines, err := DependencyLines(ScanAnnotations(text))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", scriptPath, err)
	}
	return RequiredNames(lines), nil
}
