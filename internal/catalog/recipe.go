package catalog

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/utils"
)

// Entry is what the index records about a recipe: enough to resolve a name and
// walk the dependency graph without fetching anything.
type Entry struct {
	Name           string              `json:"-"` // the index key
	Path           string              `json:"path"`
	Type           artifact.Type       `json:"type"`
	Description    string              `json:"description,omitempty"`
	URL            string              `json:"url,omitempty"`
	Deps           []string            `json:"deps,omitempty"` // #DEP: order, as written
	Arch           []Arch              `json:"arch,omitempty"` // #ARCH: values as written; empty means any architecture
	IsTemplate     bool                `json:"is_template,omitempty"`
	TargetTemplate string              `json:"target_template,omitempty"`
	PH             map[string][]string `json:"ph,omitempty"` // value order is load-bearing, key order is not
}

// Recipe is an Entry plus everything only reading the file can tell.
type Recipe struct {
	Entry
	Env        []EnvVar
	Sources    []SourceURL // #SOURCE: declarations, in order
	Inputs     []string    // #INPUT: prompts, in order
	Directives []string    // #SBATCH / #PBS / #BSUB, verbatim
	// License is #LICENSE: as written — an SPDX expression, kept verbatim and
	// never parsed. Deriving redistribution permission from a licence
	// expression is a judgement a tool gets wrong in the permissive direction,
	// and that direction cannot be taken back, so this documents and decides
	// nothing. Redistribute is the decision.
	License string
	// Redistribute is #REDISTRIBUTE: lower-cased, "" when the recipe did not
	// answer. Validate rejects anything but yes or no; Redistributable reads it
	// as the tri-state the answer actually is.
	Redistribute string
	// DeclaredType is #TYPE: lower-cased and verbatim, "" when the recipe did
	// not declare one. Type holds what DeriveType made of it, which for anything
	// but app or data is the path-based default — so this is kept for Validate
	// to reject the declaration rather than let it read as silence.
	DeclaredType string
	// Text is the recipe as fetched, tokens and all. It is what an artifact
	// embeds and what a rebuild starts from.
	Text []byte
	// Rendered is Text with a template's placeholders substituted — what the
	// build runs. Empty for a recipe that is not a template.
	Rendered []byte
	// placeholderProblems are the #PH:, #TARGET: and #SOURCE: disagreements found
	// while parsing, kept for Validate: Expand rewrites PH, so they cannot be
	// found again afterwards.
	placeholderProblems []string
}

// Redistributable reports the recipe's #REDISTRIBUTE: answer, or nil when it did not answer.
//   - Three states, not two: an absent declaration is a different fact from a considered "no" and defaults by the artifact's type.
//   - A caller that flattens nil to false refuses everything nobody has annotated yet.
func (r *Recipe) Redistributable() *bool {
	switch r.Redistribute {
	case "yes":
		yes := true
		return &yes
	case "no":
		no := false
		return &no
	}
	return nil
}

// Script returns the bytes to execute: the rendered copy for an expanded
// template, the recipe itself otherwise.
func (r *Recipe) Script() []byte {
	if len(r.Rendered) > 0 {
		return r.Rendered
	}
	return r.Text
}

// Arch is one value of #ARCH:.
//   - amd64 and arm64 name an architecture the recipe may be built on.
//   - noarch declares the result portable. Nothing can work that out by inspection — only the author knows whether a build produced machine code, a tool-specific binary dump, or bytes that mean the same everywhere — so portability must be declared.
type Arch string

const (
	ArchAMD64  Arch = "amd64"
	ArchARM64  Arch = "arm64"
	ArchNoarch Arch = "noarch"
)

// Noarch reports whether the recipe declares its result portable.
func (e *Entry) Noarch() bool { return slices.Contains(e.Arch, ArchNoarch) }

// BuildableOn reports whether the recipe may be built on arch.
//   - A recipe with no #ARCH: builds anywhere, and so does a noarch one.
//   - A list names the architectures it builds on.
func (e *Entry) BuildableOn(arch string) error {
	if len(e.Arch) == 0 || e.Noarch() || slices.Contains(e.Arch, Arch(arch)) {
		return nil
	}
	return fmt.Errorf("%w: %s builds on %s only; this machine is %s",
		ErrUnsupportedArch, e.Name, joinArch(e.Arch), arch)
}

func joinArch(archs []Arch) string {
	names := make([]string, len(archs))
	for i, a := range archs {
		names[i] = string(a)
	}
	return strings.Join(names, " and ")
}

// parseArch reads a #ARCH: value as a list separated by spaces or commas.
func parseArch(value string) []Arch {
	var out []Arch
	for _, field := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == ' ' || r == ',' || r == '\t' }) {
		out = append(out, Arch(field))
	}
	return out
}

// EnvVar is one #ENV: contribution. Segments keep {prefix} intact, since the
// mount path is not known until the artifact is loaded.
type EnvVar struct {
	Key      string
	Segments []Segment
	Note     string // the ## note
}

// Segment is a run of literal text, or a {token} when IsToken.
type Segment struct {
	Text    string
	IsToken bool
}

// Value renders the segments with tokens substituted from vars. A token with no
// entry is left in braces, which is how {prefix} survives to the manifest.
func (e EnvVar) Value(vars map[string]string) string {
	var b strings.Builder
	for _, s := range e.Segments {
		switch v, ok := vars[s.Text]; {
		case !s.IsToken:
			b.WriteString(s.Text)
		case ok:
			b.WriteString(v)
		default:
			b.WriteByte('{')
			b.WriteString(s.Text)
			b.WriteByte('}')
		}
	}
	return b.String()
}

// ParseRecipe reads a recipe's headers and body. path is relative to the source
// root and carries what the file cannot: the module name, and whether it is a
// .def. A leading recipes/ is optional, so an index path and a bare module path
// both work.
func ParseRecipe(path string, r io.Reader) (*Recipe, error) {
	text, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	isDef := strings.HasSuffix(path, ".def")
	rec := &Recipe{Text: text}
	rec.Path = path
	rec.Name = strings.TrimPrefix(strings.TrimSuffix(path, ".def"), recipesDir+"/")

	var declared string
	utils.WarnStray(path, utils.StrayAnnotations(text, recipeKeys...))
	rec.Directives = scanDirectives(text)
	for _, a := range utils.ScanAnnotations(text) {
		value, note := a.Value, a.Note

		switch a.Key {
		case "#TYPE":
			declared = strings.ToLower(value)
			rec.DeclaredType = declared
		case "#ARCH":
			if rec.Arch == nil {
				rec.Arch = parseArch(value)
			}
		case "#DESC":
			rec.Description = firstOf(rec.Description, value)
		case "#URL":
			rec.URL = firstOf(rec.URL, value)
		case "#LICENSE":
			rec.License = firstOf(rec.License, value)
		case "#REDISTRIBUTE":
			rec.Redistribute = firstOf(rec.Redistribute, strings.ToLower(strings.TrimSpace(value)))
		case "#TARGET":
			rec.TargetTemplate = firstOf(rec.TargetTemplate, value)
		case "#DEP":
			if value != "" {
				rec.Deps = append(rec.Deps, utils.NormalizeModule(value))
			}
		case "#ENV":
			if env, ok := parseEnv(value, note); ok {
				rec.Env = append(rec.Env, env)
			}
		case "#SOURCE":
			if src, ok := parseSource(value); ok {
				rec.Sources = append(rec.Sources, src)
			}
		case "#INPUT":
			if prompt := strings.TrimSpace(value); prompt != "" {
				rec.Inputs = append(rec.Inputs, prompt)
			}
		case "#PH":
			name, values, problem := parsePH(value)
			if problem != "" {
				rec.placeholderProblems = append(rec.placeholderProblems, problem)
				continue
			}
			if rec.PH == nil {
				rec.PH = map[string][]string{}
			}
			if _, dup := rec.PH[name]; !dup {
				rec.PH[name] = values
			}
		}
	}

	rec.placeholderProblems = append(rec.placeholderProblems, ValidateTemplate(rec.TargetTemplate, rec.PH)...)
	rec.placeholderProblems = append(rec.placeholderProblems, sourcePlaceholderProblems(rec.Sources, rec.PH)...)
	rec.IsTemplate = len(rec.PH) > 0 && rec.TargetTemplate != ""
	rec.Type = DeriveType(rec.Name, rec.TargetTemplate, isDef, declared)
	return rec, nil
}

// recipeKeys are the headers ParseRecipe reads. One after the header block is reported, not dropped silently.
var recipeKeys = []string{"#TYPE", "#ARCH", "#DESC", "#URL", "#LICENSE", "#REDISTRIBUTE", "#TARGET", "#DEP", "#ENV", "#SOURCE", "#INPUT", "#PH"}

// scanDirectives returns every scheduler directive line of the header block, verbatim.
//   - These are not annotations: `#SBATCH --time=01:00:00` has no `#KEY: value` shape, and cutting it on the first colon would split the value.
//   - They are matched by prefix and handed to the scheduler packages to parse.
func scanDirectives(text []byte) []string {
	var out []string
	lines := strings.Split(string(text), "\n")
	for _, line := range lines[:utils.HeaderEnd(lines)] {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		switch {
		case strings.HasPrefix(line, "#SBATCH"),
			strings.HasPrefix(line, "#PBS"),
			strings.HasPrefix(line, "#BSUB"):
			out = append(out, line)
		}
	}
	return out
}

// SourceURL is one #SOURCE: declaration: a name the recipe body refers to as
// $CNT_BUILD_SRC_<name>, and where the file comes from.
//
// A link only the user holds — expiring, per-user, EULA-gated — is declared with
// a Prompt instead of a URL, and the tool asks for it. The answer is never
// recorded. Arch, when set, restricts the line to one architecture.
type SourceURL struct {
	Name   string
	Arch   string
	URL    string
	Prompt string
}

// askPrefix marks a #SOURCE: whose link is asked for rather than written down.
const askPrefix = "ask:"

// SourceArches are the architectures a #SOURCE: may name, spelled as Go does.
var SourceArches = []string{"amd64", "arm64"}

// ErrNoSourceForArch is returned when a source is declared per architecture and
// none of its lines is for the one asked about.
var ErrNoSourceForArch = errors.New("no #SOURCE: for this architecture")

// parseSource splits "name url", "name ask:prompt", or either with an architecture word between the name and the rest.
//   - The name becomes an environment variable, so it is restricted to what one can hold.
//   - A malformed declaration is skipped here and reported by Validate, which is where an author is told rather than left with an unset variable.
func parseSource(value string) (SourceURL, bool) {
	name, rest, _ := strings.Cut(strings.TrimSpace(value), " ")
	rest = strings.TrimSpace(rest)
	if !validSourceName(name) || rest == "" {
		return SourceURL{}, false
	}
	arch := ""
	if word, tail, found := strings.Cut(rest, " "); found && isSourceArch(word) {
		arch, rest = word, strings.TrimSpace(tail)
	}
	if prompt, ok := strings.CutPrefix(rest, askPrefix); ok {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return SourceURL{}, false
		}
		return SourceURL{Name: name, Arch: arch, Prompt: prompt}, true
	}
	if rest == "" || strings.ContainsAny(rest, " \t") {
		return SourceURL{}, false
	}
	return SourceURL{Name: name, Arch: arch, URL: rest}, true
}

func isSourceArch(word string) bool {
	for _, a := range SourceArches {
		if word == a {
			return true
		}
	}
	return false
}

// SourcesFor returns the #SOURCE: lines that apply on arch, one per name, in
// declaration order. A name declared only for other architectures is an error.
func (r *Recipe) SourcesFor(arch string) ([]SourceURL, error) {
	var out []SourceURL
	for _, src := range r.Sources {
		if src.Arch == "" || src.Arch == arch {
			out = append(out, src)
		}
	}
	for _, src := range r.Sources {
		if !hasSourceName(out, src.Name) {
			return out, fmt.Errorf("%w: #SOURCE:%s has no line for %s", ErrNoSourceForArch, src.Name, arch)
		}
	}
	return out, nil
}

func hasSourceName(sources []SourceURL, name string) bool {
	for _, src := range sources {
		if src.Name == name {
			return true
		}
	}
	return false
}

// Prompts returns every question a build on arch must put to the user, in the
// order the answers are supplied: the #INPUT: prompts, then each #SOURCE: ask:
// that applies on arch, in declaration order. #INPUT: answers reach the recipe;
// the rest are consumed by the fetch and never reach it.
func (r *Recipe) Prompts(arch string) []string {
	out := append([]string(nil), r.Inputs...)
	sources, _ := r.SourcesFor(arch)
	for _, src := range sources {
		if src.Prompt != "" {
			out = append(out, src.Prompt)
		}
	}
	return out
}

// scanMalformedSources returns the #SOURCE: values parseSource refused, as
// written. Parsing skips them so one bad line cannot take a collection out of a
// listing; Validate reads them back here to tell the author.
func scanMalformedSources(text []byte) []string {
	var out []string
	for _, a := range utils.ScanAnnotations(text) {
		if a.Key != "#SOURCE" {
			continue
		}
		if _, ok := parseSource(a.Value); !ok {
			out = append(out, a.Value)
		}
	}
	return out
}

// validSourceName reports whether name can be the tail of $CNT_BUILD_SRC_<name>.
func validSourceName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return name[0] < '0' || name[0] > '9'
}

// parseEnv splits KEY=value and tokenizes the value on {name}.
func parseEnv(value, note string) (EnvVar, bool) {
	key, raw, ok := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return EnvVar{}, false
	}
	return EnvVar{Key: key, Segments: tokenize(raw), Note: note}, true
}

// tokenize splits text into literal runs and {token} runs.
func tokenize(text string) []Segment {
	var out []Segment
	for text != "" {
		open := strings.IndexByte(text, '{')
		if open < 0 {
			return append(out, Segment{Text: text})
		}
		close := strings.IndexByte(text[open:], '}')
		if close < 0 {
			return append(out, Segment{Text: text})
		}
		close += open
		if open > 0 {
			out = append(out, Segment{Text: text[:open]})
		}
		out = append(out, Segment{Text: text[open+1 : close], IsToken: true})
		text = text[close+1:]
	}
	return out
}

// firstOf keeps the first value a repeated header set.
func firstOf(have, next string) string {
	if have != "" {
		return have
	}
	return next
}
