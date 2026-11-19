// Package slot reads the #SLOT: headers of a script, binds each slot to an overlay, and checks what the bound overlay provides.
package slot

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// ErrInvalidSlot is returned for a #SLOT: header that is not usable.
var ErrInvalidSlot = errors.New("invalid slot declaration")

// Decl is one #SLOT: declaration.
type Decl struct {
	// Name is the slot name, lower case letters, digits and underscores.
	Name string
	// Default is the overlay used when nothing is bound. Empty when the slot has none.
	Default string
	// Choices are the overlays the script supports. Empty means the slot is open.
	Choices []string
	// Aliases maps a short name written in choices= to its overlay. A short name stands for that overlay in --slot and default=.
	Aliases map[string]string
	// Group names the set of slots that one --slot value binds together by short name. Empty when the slot is in none.
	Group string
	// Strict is true unless strict=false. A strict slot with choices binds only those.
	Strict bool
	// Env lists the variables the bound overlay must export.
	Env []string
	// Bin lists the commands the bound overlay must put on PATH.
	Bin []string
	// Optional lets the slot stay unbound.
	Optional bool
	// Line is the 1-based line of the header.
	Line int
}

var (
	slotName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	envName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Parse reads every #SLOT: annotation of a script, in the order written. optional= is not an option there: a script's slot is always bound.
//   - A problem is an error naming the line. What did parse is returned with it.
//   - The value is the slot name, and a name written twice is refused.
//   - default= and each choices= entry are overlay names or files. A version constraint or a {KEY} token is refused.
//   - The default must be one of choices=, strict= needs choices=, and optional=true cannot have a default.
//   - group= joins slots that one --slot value binds by short name. Such a slot needs a short name on every choice. A helper does not read it.
//   - The slots of a group list the same short names, and their defaults are all given or all left out, each with one short name. A group name is not a slot name.
func Parse(annotations []utils.Annotation) ([]Decl, error) {
	return parseAll(annotations, false)
}

// ParseHelper is Parse for a helper, where optional=true lets a slot stay unbound and group= is not an option.
func ParseHelper(annotations []utils.Annotation) ([]Decl, error) {
	return parseAll(annotations, true)
}

func parseAll(annotations []utils.Annotation, helper bool) ([]Decl, error) {
	var decls []Decl
	var problems []error
	seen := map[string]int{}
	for _, a := range utils.SelectAnnotations(annotations, "#SLOT") {
		d, err := parseOne(a, helper)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if first, dup := seen[d.Name]; dup {
			problems = append(problems, fmt.Errorf("%w: line %d: slot %q is already declared on line %d", ErrInvalidSlot, a.Line, d.Name, first))
			continue
		}
		seen[d.Name] = a.Line
		decls = append(decls, d)
	}
	problems = append(problems, checkGroups(decls)...)
	return decls, errors.Join(problems...)
}

// checkGroups reports the rules that tie the slots of a group together.
func checkGroups(decls []Decl) []error {
	var problems []error
	members := map[string][]Decl{}
	var order []string
	for _, d := range decls {
		if d.Group == "" {
			continue
		}
		if _, ok := members[d.Group]; !ok {
			order = append(order, d.Group)
		}
		members[d.Group] = append(members[d.Group], d)
	}
	for _, group := range order {
		list := members[group]
		fail := func(d Decl, format string, args ...any) {
			problems = append(problems, fmt.Errorf("%w: line %d: group %q: %s", ErrInvalidSlot, d.Line, group, fmt.Sprintf(format, args...)))
		}
		if slices.ContainsFunc(decls, func(d Decl) bool { return d.Name == group }) {
			fail(list[0], "the name is also the slot %q", group)
		}
		first := list[0]
		names := slices.Sorted(maps.Keys(first.Aliases))
		var shortDefault string
		for _, d := range list {
			if got := slices.Sorted(maps.Keys(d.Aliases)); !slices.Equal(got, names) {
				fail(d, "slot %q lists the short names %s, but slot %q lists %s", d.Name, strings.Join(got, ","), first.Name, strings.Join(names, ","))
			}
			if (d.Default == "") != (first.Default == "") {
				fail(d, "slots %q and %q must both have a default= or both have none", first.Name, d.Name)
				continue
			}
			if d.Default == "" {
				continue
			}
			short, _ := d.ShortName(d.Default)
			switch {
			case shortDefault == "":
				shortDefault = short
			case short != shortDefault:
				fail(d, "slot %q defaults to %q and slot %q to %q; the defaults must have one short name", d.Name, short, first.Name, shortDefault)
			}
		}
	}
	return problems
}

func parseOne(a utils.Annotation, helper bool) (Decl, error) {
	fail := func(format string, args ...any) (Decl, error) {
		return Decl{}, fmt.Errorf("%w: line %d: %s", ErrInvalidSlot, a.Line, fmt.Sprintf(format, args...))
	}
	accepts := []string{"default", "choices", "strict", "env", "bin"}
	if helper {
		accepts = append(accepts, utils.OptionOptional)
	} else {
		accepts = append(accepts, "group")
	}
	name, opts, err := utils.SplitOptions(a, accepts...)
	if err != nil {
		return Decl{}, err
	}
	if !slotName.MatchString(name) {
		return fail("%q is not a slot name; use lower case letters, digits and underscores", name)
	}
	d := Decl{Name: name, Strict: true, Optional: opts.Bool(utils.OptionOptional), Line: a.Line}
	if v, ok := opts["strict"]; ok {
		d.Strict = v == "true"
	}
	if _, ok := opts["strict"]; ok && opts["choices"] == "" {
		return fail("strict= needs choices=, since there is nothing to restrict")
	}
	if list, ok := opts["choices"]; ok {
		entries, err := splitList(list)
		if err != nil {
			return fail("choices=: %v", err)
		}
		for _, entry := range entries {
			short, overlay, named := strings.Cut(entry, "=")
			if !named {
				overlay = entry
			} else if !slotName.MatchString(short) {
				return fail("choices=%s: %q is not a short name; use lower case letters, digits and underscores", entry, short)
			}
			if err := checkOverlayName(overlay); err != nil {
				return fail("choices=%s: %v", entry, err)
			}
			d.Choices = append(d.Choices, overlay)
			if named {
				if d.Aliases == nil {
					d.Aliases = map[string]string{}
				}
				if _, dup := d.Aliases[short]; dup {
					return fail("choices=: the short name %q is written twice", short)
				}
				d.Aliases[short] = overlay
			}
		}
		for short := range d.Aliases {
			if slices.Contains(d.Choices, short) {
				return fail("choices=: the short name %q is also an overlay in the list", short)
			}
		}
	}
	if v := opts["default"]; v != "" {
		if overlay, ok := d.Aliases[v]; ok {
			v = overlay
		}
		if err := checkOverlayName(v); err != nil {
			return fail("default=%s: %v", opts["default"], err)
		}
		d.Default = v
		if len(d.Choices) > 0 && !d.Takes(d.Default) {
			return fail("default=%s is not one of choices=%s", opts["default"], strings.Join(d.Choices, ","))
		}
	}
	if d.Optional && d.Default != "" {
		return fail("optional=true cannot have a default=, since a default would bind the slot")
	}
	if group, ok := opts["group"]; ok {
		switch {
		case !slotName.MatchString(group):
			return fail("group=%s is not a group name; use lower case letters, digits and underscores", group)
		case len(d.Choices) == 0 || len(d.Aliases) != len(d.Choices):
			return fail("group=%s needs choices= with a short name on every choice", group)
		}
		d.Group = group
	}
	if list, ok := opts["env"]; ok {
		if d.Env, err = splitList(list); err != nil {
			return fail("env=: %v", err)
		}
		for _, v := range d.Env {
			if !envName.MatchString(v) {
				return fail("env=%s is not a variable name", v)
			}
		}
	}
	if list, ok := opts["bin"]; ok {
		if d.Bin, err = splitList(list); err != nil {
			return fail("bin=: %v", err)
		}
		for _, c := range d.Bin {
			if strings.ContainsAny(c, "/ \t") {
				return fail("bin=%s is not a command name", c)
			}
		}
	}
	return d, nil
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

// checkOverlayName refuses what a slot cannot bind: a version constraint, a {KEY} token or a `=`, which introduces a short name. A file passes as written.
func checkOverlayName(v string) error {
	if utils.IsPathDep(v) {
		return nil
	}
	if strings.ContainsAny(v, "{}") {
		return errors.New("a {KEY} token is filled by a helper parameter, which a slot does not take")
	}
	dep, err := utils.ParseDep(v)
	if err != nil {
		return err
	}
	if dep.Op != "" {
		return errors.New("a version constraint is for a build recipe; name the overlay or its version")
	}
	if strings.Contains(v, "=") {
		return errors.New("write the overlay as name/version; = introduces a short name in choices=")
	}
	return nil
}

// Takes reports whether overlay is exactly one of the choices. A slot with no choices takes any overlay.
//   - A short name is not an overlay. Bind turns it into its overlay first.
func (d Decl) Takes(overlay string) bool {
	if len(d.Choices) == 0 {
		return true
	}
	overlay = normalize(overlay)
	return slices.ContainsFunc(d.Choices, func(c string) bool { return normalize(c) == overlay })
}

// ShortName is the short name of overlay among the choices. It reports false for an overlay that is not a choice or has none.
func (d Decl) ShortName(overlay string) (string, bool) {
	overlay = normalize(overlay)
	for short, full := range d.Aliases {
		if normalize(full) == overlay {
			return short, true
		}
	}
	return "", false
}

// ChoiceLabels lists the choices as written, a short name first: `mouse=grcm39/genome/gencode`.
func (d Decl) ChoiceLabels() []string {
	short := map[string]string{}
	for name, overlay := range d.Aliases {
		short[overlay] = name
	}
	out := make([]string, len(d.Choices))
	for i, c := range d.Choices {
		out[i] = c
		if name, ok := short[c]; ok {
			out[i] = name + "=" + c
		}
	}
	return out
}

// Covers reports whether name is one of choices or a more specific version of one.
//   - A choice covers the names that extend it by whole parts: grch38/genome/gencode covers grch38/genome/gencode/49 and not grch38/genome/gencode2.
//   - A file is covered only by itself.
func Covers(choices []string, name string) bool {
	name = normalize(name)
	for _, c := range choices {
		c = normalize(c)
		if name == c || strings.HasPrefix(name, c+"/") {
			return true
		}
	}
	return false
}

func normalize(v string) string {
	if utils.IsPathDep(v) {
		return v
	}
	return utils.NormalizeModule(v)
}

// ParseScript reads the #SLOT: headers of the script at path.
//   - Only the header block is read, and a #SLOT: after the first command is warned about and ignored.
func ParseScript(path string) ([]Decl, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read script: %w", err)
	}
	utils.WarnStray(path, utils.StrayAnnotations(data, "#SLOT"))
	decls, err := Parse(utils.ScanAnnotations(data))
	if err != nil {
		return decls, fmt.Errorf("%s: %w", path, err)
	}
	return decls, nil
}
