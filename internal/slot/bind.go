package slot

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Source says where a binding came from.
type Source string

const (
	FromFlag    Source = "flag"
	FromGroup   Source = "group"
	FromDefault Source = "default"
)

// Binding is a slot and the overlay chosen for it.
type Binding struct {
	Decl
	// Requested is the overlay as typed or declared. Empty for an optional slot left unbound.
	Requested string
	Source    Source
	// Unlisted is set when a strict slot was given an overlay its choices do not list as written.
	// The overlay the value resolves to is checked once it is known, so a partial tool or os name can still reach a choice.
	Unlisted bool
}

// Bound reports whether the slot has an overlay.
func (b Binding) Bound() bool { return b.Requested != "" }

// ErrUnbound is matched by the error Bind returns when a slot has nothing to bind.
var ErrUnbound = errors.New("slot has no overlay")

// UnboundError lists the slots that have no default and were not bound.
type UnboundError struct {
	Slots []Decl
}

func (e *UnboundError) Error() string {
	names := make([]string, len(e.Slots))
	for i, d := range e.Slots {
		names[i] = d.Name
	}
	return fmt.Sprintf("%v: bind %s with --slot NAME=OVERLAY", ErrUnbound, strings.Join(names, ", "))
}

func (e *UnboundError) Is(target error) bool { return target == ErrUnbound }

// Refusal is the error for a value that resolved to an overlay the slot does not take. resolved is empty when nothing installed answers the value.
func (b Binding) Refusal(resolved string) error {
	what := b.Requested
	if resolved != "" && resolved != b.Requested {
		what += " (" + resolved + ")"
	}
	return fmt.Errorf("slot %q cannot be bound to %s; it takes %s (strict=false allows any overlay)",
		b.Name, what, strings.Join(b.ChoiceLabels(), ", "))
}

// CheckResolved refuses an unlisted value whose resolved overlay is not one of the choices.
func (b Binding) CheckResolved(resolved string) error {
	if !b.Unlisted || b.Takes(resolved) {
		return nil
	}
	return b.Refusal(resolved)
}

// Bind chooses an overlay for each slot from the --slot values, written NAME=OVERLAY.
//   - A value that names neither a slot nor a group the script declares is refused, listing both.
//   - A slot or group written twice is refused.
//   - A group takes a short name, and each of its slots takes the choice with that short name.
//   - A group is not written with one of its slots, and if one slot of a group is written, all of them are.
//   - An unbound slot takes its default. With none it stays unbound when optional, and is returned with no overlay. Otherwise it is reported in an *UnboundError.
//   - A short name from choices= stands for its overlay.
//   - A strict slot takes a choice by its full name or its short name. Any other value is marked Unlisted, and its resolved overlay must be a choice: strict limits the choices, not how a name is completed.
//   - The bindings are returned in the order the slots are declared. With an *UnboundError they hold every slot that did bind.
func Bind(decls []Decl, values []string) ([]Binding, error) {
	given := map[string]string{}
	groups := map[string]string{}
	for _, v := range values {
		name, overlay, ok := strings.Cut(v, "=")
		name, overlay = strings.TrimSpace(name), strings.TrimSpace(overlay)
		if !ok || name == "" || overlay == "" {
			return nil, fmt.Errorf("--slot %q is not NAME=OVERLAY", v)
		}
		if members := groupMembers(decls, name); len(members) > 0 && !declared(decls, name) {
			if _, ok := members[0].Aliases[overlay]; !ok {
				return nil, fmt.Errorf("group %q takes %s, not %q", name, strings.Join(slices.Sorted(maps.Keys(members[0].Aliases)), ", "), overlay)
			}
			if _, dup := groups[name]; dup {
				return nil, fmt.Errorf("group %q is bound twice", name)
			}
			groups[name] = overlay
			continue
		}
		if !declared(decls, name) {
			what := "slot"
			if len(groupNames(decls)) > 0 {
				what = "slot or group"
			}
			return nil, fmt.Errorf("no %s %q is declared; %s", what, name, declaredList(decls))
		}
		if _, dup := given[name]; dup {
			return nil, fmt.Errorf("slot %q is bound twice", name)
		}
		given[name] = overlay
	}
	if err := checkGroupValues(decls, given, groups); err != nil {
		return nil, err
	}

	var out []Binding
	var unbound []Decl
	for _, d := range decls {
		b := Binding{Decl: d}
		switch overlay, ok := given[d.Name]; {
		case d.Group != "" && groups[d.Group] != "":
			b.Requested, b.Source = d.Aliases[groups[d.Group]], FromGroup
		case ok:
			if full, short := d.Aliases[overlay]; short {
				overlay = full
			}
			b.Unlisted = d.Strict && !d.Takes(overlay)
			b.Requested, b.Source = overlay, FromFlag
		case d.Default != "":
			b.Requested, b.Source = d.Default, FromDefault
		case d.Optional:
		default:
			unbound = append(unbound, d)
			continue
		}
		out = append(out, b)
	}
	if len(unbound) > 0 {
		return out, &UnboundError{Slots: unbound}
	}
	return out, nil
}

func declared(decls []Decl, name string) bool {
	for _, d := range decls {
		if d.Name == name {
			return true
		}
	}
	return false
}

func declaredList(decls []Decl) string {
	if len(decls) == 0 {
		return "the script declares no slots"
	}
	names := make([]string, len(decls))
	for i, d := range decls {
		names[i] = d.Name
	}
	for _, g := range groupNames(decls) {
		names = append(names, g+" (group)")
	}
	return "the script declares " + strings.Join(names, ", ")
}

// groupNames lists the groups in the order their first slot is declared.
func groupNames(decls []Decl) []string {
	var out []string
	for _, d := range decls {
		if d.Group != "" && !slices.Contains(out, d.Group) {
			out = append(out, d.Group)
		}
	}
	return out
}

// groupMembers are the slots of a group, in declaration order.
func groupMembers(decls []Decl, group string) []Decl {
	var out []Decl
	for _, d := range decls {
		if d.Group == group {
			out = append(out, d)
		}
	}
	return out
}

// checkGroupValues refuses a group written with one of its slots, and a group of which only some slots are written.
func checkGroupValues(decls []Decl, given, groups map[string]string) error {
	for _, group := range groupNames(decls) {
		members := groupMembers(decls, group)
		var set, unset []string
		for _, d := range members {
			if _, ok := given[d.Name]; ok {
				set = append(set, d.Name)
			} else {
				unset = append(unset, d.Name)
			}
		}
		switch {
		case groups[group] != "" && len(set) > 0:
			return fmt.Errorf("group %q is written with its slot %s; write the group, or write every slot of it", group, strings.Join(set, ", "))
		case groups[group] == "" && len(set) > 0 && len(unset) > 0:
			return fmt.Errorf("slot %s is written but %s, in group %q, is not; write %s too, or write the group with --slot %s=%s",
				strings.Join(set, ", "), strings.Join(unset, ", "), group, strings.Join(unset, ", "), group, strings.Join(slices.Sorted(maps.Keys(members[0].Aliases)), "|"))
		}
	}
	return nil
}

// GroupValues maps each group to the short name that all of its slots are bound to, given the exact overlay each slot is bound to.
//   - A group whose slots are not all bound to one short name, such as when a slot holds an overlay outside its choices, has no value.
func GroupValues(decls []Decl, overlays map[string]string) map[string]string {
	values := map[string]string{}
	broken := map[string]bool{}
	for _, d := range decls {
		if d.Group == "" {
			continue
		}
		short, ok := d.ShortName(overlays[d.Name])
		if prev, seen := values[d.Group]; !ok || (seen && prev != short) {
			broken[d.Group] = true
			continue
		}
		values[d.Group] = short
	}
	for group := range broken {
		delete(values, group)
	}
	return values
}
