package helper

import (
	"context"
	"fmt"

	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/slot"
)

// SlotMount is a bound slot and the overlay it mounts.
type SlotMount struct {
	Binding slot.Binding
	// Name is the exact overlay name.
	Name string
	Path string
}

// ResolveSlots binds the helper's slots from values, written SLOT=OVERLAY, and resolves each to an overlay file.
//   - A slot with no default and no value is a *slot.UnboundError.
//   - A missing overlay is built before the job starts, as for a required #DEP:.
//   - Each overlay is checked for the variables and commands its slot needs, and two slots may not export one variable differently.
//   - The mounts come in the order the slots are declared. A slot left unbound mounts nothing.
func ResolveSlots(ctx context.Context, cwd string, decls []slot.Decl, values []string, noProject bool) ([]SlotMount, error) {
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
	if len(bound) == 0 {
		return nil, nil
	}
	// A value a strict slot does not list is resolved first, with nothing built, so a name the slot does not take is never built.
	// The rest are built when missing, as a required #DEP: is.
	paths := make([]string, len(bound))
	var build []int
	for i, b := range bound {
		if !b.Unlisted {
			build = append(build, i)
			continue
		}
		found, err := ResolveOptionalOverlays(ctx, cwd, []string{b.Requested}, nil, noProject)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 || found[0].Path == "" {
			return nil, b.Refusal("")
		}
		paths[i] = found[0].Path
	}
	if len(build) > 0 {
		names := make([]string, len(build))
		for k, i := range build {
			names[k] = requests[i]
		}
		got, err := CheckRequiredOverlays(ctx, cwd, names, nil, noProject)
		if err != nil {
			return nil, err
		}
		for k, i := range build {
			paths[i] = got[k]
		}
	}
	resolved := make([]slot.Resolved, len(bound))
	mounts := make([]SlotMount, len(bound))
	for i, b := range bound {
		resolved[i] = slot.Resolved{Binding: b, Path: paths[i]}
		name, err := slot.Check(resolved[i])
		if err != nil {
			return nil, err
		}
		if err := b.CheckResolved(name); err != nil {
			return nil, err
		}
		mounts[i] = SlotMount{Binding: b, Name: name, Path: paths[i]}
	}
	if err := slot.Collisions(resolved); err != nil {
		return nil, err
	}
	noteUnpinnedSlots(ctx, cwd, bound, requests, noProject)
	return mounts, nil
}

// noteUnpinnedSlots says which slot overlays no pin of the standing project answers. They mount from what is installed and are not recorded.
func noteUnpinnedSlots(ctx context.Context, cwd string, bound []slot.Binding, requests []string, noProject bool) {
	if noProject {
		return
	}
	standing, err := project.StandingAt(cwd)
	if err != nil || standing == nil {
		return
	}
	found, err := standing.ResolveNames(ctx, requests)
	if err != nil {
		return
	}
	logger := logging.FromContext(ctx)
	for i, m := range found {
		if m.Live {
			logger.Info(fmt.Sprintf("slot %s: %s is not pinned in the project, so the installed overlay is mounted", bound[i].Name, m.Name), "kind", "note")
		}
	}
}

// slotPaths are the overlay files of the mounts, in order.
func slotPaths(mounts []SlotMount) []string {
	out := make([]string, len(mounts))
	for i, m := range mounts {
		out[i] = m.Path
	}
	return out
}

// SlotNames maps each bound slot to the exact overlay it mounts. It is nil when no slot is bound.
func (p *RunPlan) SlotNames() map[string]string {
	if len(p.Slots) == 0 {
		return nil
	}
	names := make(map[string]string, len(p.Slots))
	for _, m := range p.Slots {
		names[m.Binding.Name] = m.Name
	}
	return names
}

// RestoreSlots turns the slots a past run mounted into --slot values for the slots the script declares now.
//   - The values come in the order the slots are declared, each with the exact overlay that run mounted.
func RestoreSlots(decls []slot.Decl, recorded map[string]string) []string {
	var values []string
	for _, d := range decls {
		if overlay, ok := recorded[d.Name]; ok && restorable(d, overlay) {
			values = append(values, d.Name+"="+overlay)
		}
	}
	return values
}

// FilterSlots keeps the recorded slots the script declares now and still takes. It is nil when none is kept.
//   - A slot the script no longer declares, and an overlay a strict slot no longer takes, are dropped.
func FilterSlots(decls []slot.Decl, recorded map[string]string) map[string]string {
	var kept map[string]string
	for _, d := range decls {
		if overlay, ok := recorded[d.Name]; ok && restorable(d, overlay) {
			if kept == nil {
				kept = map[string]string{}
			}
			kept[d.Name] = overlay
		}
	}
	return kept
}

func restorable(d slot.Decl, overlay string) bool {
	return !d.Strict || d.Takes(overlay)
}
