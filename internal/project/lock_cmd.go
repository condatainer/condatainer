package project

import (
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/helperhistory"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
)

// ProjectStatus is a plain, read-only view of a project — the CLI's
// `project status` and the dashboard's status panel both read this, so the
// two never describe a project's state two different ways.
type ProjectStatus struct {
	HasProject bool   `json:"has_project"`
	Root       string `json:"root,omitempty"`
	PinCount   int    `json:"pin_count"`
	// Unpinned lists #DEP:-derived declarations with no pin yet.
	Unpinned []string `json:"unpinned,omitempty"`
	// UnpinnedHelperOverlays and ManualPinUsage are the same two
	// usageIndex-derived signals project/orchestrate.Lock returns.
	UnpinnedHelperOverlays []string            `json:"unpinned_helper_overlays,omitempty"`
	ManualPinUsage         map[string][]string `json:"manual_pin_usage,omitempty"`
}

// StatusAt reads status for the project rooted exactly at root — no walking
// up to an ancestor. root must already contain cnt-lock/; ErrNoProject
// otherwise (matching lock.RootAt).
func StatusAt(root string) (*ProjectStatus, error) {
	if _, err := lock.RootAt(root); err != nil {
		return nil, err
	}
	l, err := lock.Load(root)
	if err != nil {
		return nil, err
	}
	return statusAt(root, l)
}

// statusAt is Status/StatusAt's shared body once a root and its loaded lock
// are already in hand — the only difference between the two is how that
// root was found.
func statusAt(root string, l *lock.Lock) (*ProjectStatus, error) {
	scanned, err := lock.Scan(root, lock.ScanOptions{})
	if err != nil {
		return nil, err
	}
	unpinnedOverlays, err := UnpinnedHelperOverlays(root, l)
	if err != nil {
		return nil, err
	}
	usage, err := ManualPinUsage(root, l)
	if err != nil {
		return nil, err
	}
	return &ProjectStatus{
		HasProject:             true,
		Root:                   root,
		PinCount:               len(l.Pins),
		Unpinned:               unpinnedRequests(l, scanned),
		UnpinnedHelperOverlays: unpinnedOverlays,
		ManualPinUsage:         usage,
	}, nil
}

// unpinnedRequests lists pinnable declarations with no pin yet, without
// mutating the lock the way lock.Reconcile does — Status only ever reads.
func unpinnedRequests(l *lock.Lock, scanned *lock.ScanResult) []string {
	var out []string
	for _, request := range scanned.Requests {
		if !request.Kind.Pinnable() {
			continue
		}
		if _, _, ok := lock.MatchPin(l, request); !ok {
			out = append(out, request.Key)
		}
	}
	sort.Strings(out)
	return out
}

// UnpublishedFrozenEnv lists pinned frozen-environment artifacts with no
// recorded remote — what `project lock`'s old noteUnpublished checked for,
// minus the printing: each caller (CLI text, SSE stream) decides how to
// show it.
func UnpublishedFrozenEnv(l *lock.Lock, pinned []*lock.Pinned) []string {
	var out []string
	for _, p := range pinned {
		if p.Identity.Scheme == string(key.PayloadTreeV1) && len(l.Remotes[p.Artifact]) == 0 {
			out = append(out, p.Artifact)
		}
	}
	return out
}

// usageIndex maps each pin key a helper's recorded overlays would address to the sorted helper names that used it.
//   - UnpinnedHelperOverlays and ManualPinUsage read it.
//   - An overlay that can never be pinned contributes no key.
func usageIndex(root string) (map[string][]string, error) {
	all, err := helperhistory.ListAll(root)
	if err != nil {
		return nil, err
	}

	sets := map[string]map[string]bool{} // pin key -> set of helper names
	add := func(pinKey, helperName string) {
		set := sets[pinKey]
		if set == nil {
			set = map[string]bool{}
			sets[pinKey] = set
		}
		set[helperName] = true
	}

	for helperName, byLocation := range all {
		for location, combos := range byLocation {
			for _, combo := range combos {
				for _, overlay := range append(slices.Clone(combo.Required), combo.Overlays...) {
					if pinKey, ok := classifyOverlay(overlay, location); ok {
						add(pinKey, helperName)
					}
				}
			}
			// The frozen env.sqf autoloaded at this location, if any, is
			// folded in for free — it isn't a fourth, special case, just
			// another KindPath candidate once resolved to a path.
			if pinKey, ok := frozenEnvKey(root, location); ok {
				add(pinKey, helperName)
			}
		}
	}

	out := make(map[string][]string, len(sets))
	for pinKey, set := range sets {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		out[pinKey] = names
	}
	return out, nil
}

// classifyOverlay computes overlay's pin key from the root-relative location it was
// recorded from, or reports that nothing could pin it.
//
//   - A catalog name/version (no overlay extension) is its own key.
//   - A project-relative path that stays under the root once joined with location is a KindPath key.
//   - An absolute path, a writable .img or an escaping path is never pinnable and contributes no key.
func classifyOverlay(overlay, location string) (string, bool) {
	if !utils.IsOverlay(overlay) {
		return overlay, true
	}
	if utils.IsImg(overlay) || filepath.IsAbs(overlay) {
		return "", false
	}
	candidate := path.Clean(path.Join(location, filepath.ToSlash(overlay)))
	if candidate == ".." || strings.HasPrefix(candidate, "../") {
		return "", false
	}
	return lock.PathPrefix + candidate, true
}

// frozenEnvKey resolves location's frozen env.sqf, never the writable .img,
// which has no identity to pin, to the same KindPath key classifyOverlay would
// give an ordinary recorded overlay at that path. It isn't a fourth, special
// case, once it's the frozen form.
func frozenEnvKey(root, location string) (string, bool) {
	dir := filepath.Join(root, filepath.FromSlash(location))
	resolved := container.ResolveEnvOverlay("", dir)
	if resolved == "" || utils.IsImg(resolved) {
		return "", false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return lock.PathPrefix + path.Clean(rel), true
}

// UnpinnedHelperOverlays is usageIndex's keys with no matching entry in
// l.Pins — a suggestion, never a fallback. It never writes a pin and never
// becomes a scan Finding.
func UnpinnedHelperOverlays(root string, l *lock.Lock) ([]string, error) {
	index, err := usageIndex(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for pinKey := range index {
		if _, pinned := l.Pins[pinKey]; !pinned {
			out = append(out, pinKey)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ManualPinUsage reports, for every manual pin, which helpers have a
// recorded combination using it — a fact, never a verdict. An absent (nil)
// entry never means "unused," only "no recorded helper usage": a cold-start
// or infrequently run helper looks the same either way, and only a person
// weighing which helpers actually matter can tell those apart.
func ManualPinUsage(root string, l *lock.Lock) (map[string][]string, error) {
	index, err := usageIndex(root)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(l.Pins))
	for pinKey, entry := range l.Pins {
		if !entry.Manual {
			continue
		}
		out[pinKey] = index[pinKey]
	}
	return out, nil
}
