package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/slot"
	"github.com/condatainer/condatainer/internal/utils"
)

// slotMount is a bound slot and the overlay it mounts.
type slotMount struct {
	Binding slot.Binding
	// Name is the exact overlay name, repeated by the job so it mounts what was resolved here.
	Name string
	Path string
}

// slotPlan is a script's slots, bound to installed overlays and checked.
type slotPlan struct {
	Bindings []slot.Binding
	Mounts   []slotMount
}

// Paths are the overlay files to mount, in the order the slots are declared.
func (p *slotPlan) Paths() []string {
	if p == nil {
		return nil
	}
	paths := make([]string, len(p.Mounts))
	for i, m := range p.Mounts {
		paths[i] = m.Path
	}
	return paths
}

// Flags are the --slot arguments a submitted job runs with: every bound slot, by exact name.
func (p *slotPlan) Flags() []string {
	if p == nil {
		return nil
	}
	flags := make([]string, len(p.Mounts))
	for i, m := range p.Mounts {
		flags[i] = "--slot=" + shellQuote(m.Binding.Name+"="+m.Name)
	}
	return flags
}

// slotSubjects is what a script's conditional headers match against: every declared slot, and the exact overlay of each bound one.
//   - plan is nil when the script declares no slots, or when they could not be bound. Declared slots are then read again so a condition on one is still known.
func slotSubjects(script string, plan *slotPlan) scheduler.Subjects {
	subjects := scheduler.Subjects{Bound: map[string]string{}}
	if plan == nil {
		decls, _ := slot.ParseScript(script)
		for _, d := range decls {
			subjects.Declared = append(subjects.Declared, d.Name)
		}
		return subjects
	}
	for _, b := range plan.Bindings {
		subjects.Declared = append(subjects.Declared, b.Name)
	}
	for _, m := range plan.Mounts {
		subjects.Bound[m.Binding.Name] = m.Name
	}
	return subjects
}

// errSlotMissing lists the overlays a bound slot names that are not installed.
type errSlotMissing struct{ Names []string }

func (e *errSlotMissing) Error() string {
	return "slot overlays not installed: " + strings.Join(e.Names, ", ")
}

// planSlots reads the script's #SLOT: headers, binds them from the --slot values, and resolves each to an installed overlay.
//   - It returns nil when the script declares no slots and none was given.
//   - A slot with no default and no value is an *slot.UnboundError, and an overlay that is not installed is an *errSlotMissing.
//   - Each overlay is then checked for the variables and commands the slot needs, and two slots may not export one variable differently.
//   - Standing in a project, a name resolves through the project's pins, and one no pin answers resolves by what is installed.
func planSlots(ctx context.Context, script string, values []string, inProject bool) (*slotPlan, error) {
	decls, err := slot.ParseScript(script)
	if err != nil {
		return nil, err
	}
	if len(decls) == 0 && len(values) == 0 {
		return nil, nil
	}
	bindings, err := slot.Bind(decls, values)
	if err != nil {
		return nil, err
	}

	var bound []slot.Binding
	var requests []string
	for _, b := range bindings {
		if b.Bound() {
			bound = append(bound, b)
			requests = append(requests, b.Requested)
		}
	}
	plan := &slotPlan{Bindings: bindings}
	if len(bound) == 0 {
		return plan, nil
	}

	mounts, err := resolveSlotMounts(ctx, requests, inProject)
	if err != nil {
		return nil, err
	}
	var missing []string
	var resolved []slot.Resolved
	for i, m := range mounts {
		if m.Path == "" || m.Unpinned && !utils.FileExists(m.Path) {
			if bound[i].Unlisted {
				return nil, bound[i].Refusal("")
			}
			missing = append(missing, requests[i])
			continue
		}
		resolved = append(resolved, slot.Resolved{Binding: bound[i], Path: m.Path})
	}
	if len(missing) > 0 {
		return nil, &errSlotMissing{Names: missing}
	}
	for i, r := range resolved {
		name, err := slot.Check(r)
		if err != nil {
			return nil, err
		}
		if err := bound[i].CheckResolved(name); err != nil {
			return nil, err
		}
		plan.Mounts = append(plan.Mounts, slotMount{Binding: bound[i], Name: name, Path: r.Path})
	}
	if err := slot.Collisions(resolved); err != nil {
		return nil, err
	}
	return plan, nil
}

// resolveSlotMounts resolves overlay names as `#DEP:` does, with nothing printed.
//   - A constraint or a {placeholder} is refused, by the same grammar.
func resolveSlotMounts(ctx context.Context, requests []string, inProject bool) ([]project.Mount, error) {
	parsed := make([]lock.Request, len(requests))
	for i, value := range requests {
		request, reason := lock.ParseDeclaration(value)
		if reason != "" {
			return nil, fmt.Errorf("%s: %s", value, reason)
		}
		parsed[i] = request
	}
	opts := project.ResolveOptions{LiveResolve: true, Distro: projectDefaultDistro(), Uncached: true}
	if inProject {
		standing, err := standingProjectHere()
		if err != nil {
			return nil, err
		}
		if standing != nil {
			resolution, err := standing.ResolveComplete(ctx, parsed, opts)
			if err != nil {
				return nil, err
			}
			return resolution.Mounts, nil
		}
	}
	return project.ResolveUnlocked(ctx, parsed, opts)
}

// reportSlotError prints why the slots of a script cannot be used, with what to do, and reports whether it did.
func reportSlotError(err error) bool {
	var unbound *slot.UnboundError
	var missing *errSlotMissing
	switch {
	case errors.As(err, &unbound):
		utils.PrintError("%v", err)
	case errors.As(err, &missing):
		utils.PrintMessage("Missing slot overlays:")
		for _, n := range missing.Names {
			utils.PrintMessage("  - %s", n)
		}
		utils.PrintHint("Install them with `condatainer create <name>`, or bind another overlay with --slot.")
	default:
		return false
	}
	return true
}

// printDryRunSlots lists each slot, the overlay it binds to and why. A slot that cannot bind is listed with the reason.
func printDryRunSlots(ctx context.Context, script string, inProject bool) {
	decls, err := slot.ParseScript(script)
	if err != nil {
		fmt.Printf("%s %v\n", utils.StyleError("[ERR]"), err)
		return
	}
	if len(decls) == 0 && len(runSlots) == 0 {
		return
	}
	fmt.Printf("%s\n", utils.StyleTitle("Slots:"))
	plan, err := planSlots(ctx, script, runSlots, inProject)
	switch {
	case err == nil:
		for _, b := range plan.Bindings {
			if !b.Bound() {
				fmt.Printf("  %-12s (not bound)\n", b.Name)
			}
		}
		for _, m := range plan.Mounts {
			fmt.Printf("  %-12s %s  (%s)\n", m.Binding.Name, m.Name, m.Binding.Source)
		}
	default:
		fmt.Printf("  %s %v\n", utils.StyleError("✗"), err)
	}
}

// printDryRunConditions lists the line each conditional resource header chose, and why.
func printDryRunConditions(conditions []scheduler.Condition) {
	if len(conditions) == 0 {
		return
	}
	fmt.Printf("%s\n", utils.StyleTitle("Conditions:"))
	for _, c := range conditions {
		fmt.Printf("  %-6s %s  line %d, %s\n", c.Key, c.Value, c.Line, c.Because)
	}
}
