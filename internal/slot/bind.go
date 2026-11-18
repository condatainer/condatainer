package slot

import (
	"errors"
	"fmt"
	"strings"
)

// Source says where a binding came from.
type Source string

const (
	FromFlag    Source = "flag"
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
//   - A value that names a slot the script does not declare is refused, listing the slots it declares.
//   - A slot written twice is refused.
//   - An unbound slot takes its default. With none it stays unbound when optional, and is returned with no overlay. Otherwise it is reported in an *UnboundError.
//   - A short name from choices= stands for its overlay.
//   - A strict slot takes a choice by its full name or its short name. Any other value is marked Unlisted, and its resolved overlay must be a choice: strict limits the choices, not how a name is completed.
//   - The bindings are returned in the order the slots are declared. With an *UnboundError they hold every slot that did bind.
func Bind(decls []Decl, values []string) ([]Binding, error) {
	given := map[string]string{}
	for _, v := range values {
		name, overlay, ok := strings.Cut(v, "=")
		name, overlay = strings.TrimSpace(name), strings.TrimSpace(overlay)
		if !ok || name == "" || overlay == "" {
			return nil, fmt.Errorf("--slot %q is not NAME=OVERLAY", v)
		}
		if !declared(decls, name) {
			return nil, fmt.Errorf("no slot %q is declared; %s", name, declaredList(decls))
		}
		if _, dup := given[name]; dup {
			return nil, fmt.Errorf("slot %q is bound twice", name)
		}
		given[name] = overlay
	}

	var out []Binding
	var unbound []Decl
	for _, d := range decls {
		b := Binding{Decl: d}
		switch overlay, ok := given[d.Name]; {
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
	return "the script declares " + strings.Join(names, ", ")
}
