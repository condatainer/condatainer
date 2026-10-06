package catalog

import (
	"context"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// Resolved is what SolveName found for a raw name: the concrete name it
// means, what the catalog offers for it, and what the caller already has.
type Resolved struct {
	Name      string // concrete module name/version the raw name means
	Entry     *Entry // nil when no source provides Name
	Source    *Source
	Vars      map[string]string // set when Name came from a template
	Installed string            // the version Have reported, when one satisfies the request
}

// SolveName resolves a raw name, bare, versioned, distro-prefixed or not, to the concrete catalog module it means, or reports the catalog has none, which is the caller's Conda fallback.
//   - It never touches Conda. have and distro are supplied, never derived, so it is reachable from any caller without an import cycle.
//   - It tries raw as given first, and retries under distro/raw only once that comes up empty: using the name as typed is not a guess, a distro prefix is.
func (c Catalog) SolveName(ctx context.Context, have Have, distro, raw string) (Resolved, bool, error) {
	normalized := Normalize(raw)
	if normalized == "" || strings.Contains(normalized, "::") {
		return Resolved{}, false, nil
	}

	if res, found, err := c.solveGiven(ctx, have, normalized); found || err != nil {
		return res, found, err
	}
	if distro != "" && strings.Count(normalized, "/") <= 1 {
		return c.solveGiven(ctx, have, distro+"/"+normalized)
	}
	return Resolved{}, false, nil
}

// solveGiven resolves name exactly as written, with no distro guessing: a complete
// module first, then, for app or os only, an auto-fill scoped by segment count (an app
// name has at most one slash, an os name two). Data never reaches an auto-fill step:
// past the exact match, a name with more segments than either pool admits is not found.
func (c Catalog) solveGiven(ctx context.Context, have Have, name string) (Resolved, bool, error) {
	if dep, err := ParseDep(name); err == nil && have != nil && dep.Version != "" && slices.Contains(have(dep.Name), dep.Version) {
		res, _, err := c.lookupExact(ctx, name)
		res.Name, res.Installed = Normalize(name), dep.Version
		return res, true, err
	}
	if res, found, err := c.lookupExact(ctx, name); found || err != nil {
		return res, found, err
	}
	slashes := strings.Count(name, "/")
	if slashes <= 1 {
		if res, found, err := c.solveApp(ctx, have, name); found || err != nil {
			return res, found, err
		}
	}
	if slashes <= 2 {
		if res, found, err := c.solveOS(ctx, have, name); found || err != nil {
			return res, found, err
		}
	}
	return Resolved{}, false, nil
}

// lookupExact resolves name as a complete module: an exact index key, or a template whose placeholder is already filled.
//   - A bare template hit — the key exists, but nothing supplied its axis — does not count: that names a family, not a module, and is left for solveApp/solveOS to auto-fill.
//   - So a name that is also a template's key resolves to its latest version, never to the template.
func (c Catalog) lookupExact(ctx context.Context, name string) (Resolved, bool, error) {
	m, found, err := c.Lookup(ctx, name)
	if err != nil {
		return Resolved{}, false, err
	}
	if found && m.Entry.IsTemplate && len(m.Vars) == 0 {
		found = false
	}
	if !found {
		return Resolved{}, false, nil
	}
	return Resolved{Name: Normalize(name), Entry: m.Entry, Source: m.Source, Vars: m.Vars}, true, nil
}

// solveApp resolves name as an app, bare ("openjdk") or name/version ("openjdk/17"),
// against TypeApp candidates only: a flat sibling one segment below name, or a #PH: axis
// on a template whose #TARGET: is name/{axis}.
//
//   - have is checked first. If it answers, the catalog is never consulted: installed
//     beats a catalog lookup, and an overlay whose recipe moved keeps resolving.
//   - Only when nothing is installed does the catalog decide, and a scan that finds
//     nothing is not found.
//   - Picking the newest is safe: an app's candidates are the same tool at different
//     times, never a different tool sharing a name prefix.
func (c Catalog) solveApp(ctx context.Context, have Have, name string) (Resolved, bool, error) {
	dep, err := ParseDep(name)
	if err != nil {
		return Resolved{}, false, nil
	}

	installed := installedVersion(have, dep)
	version, fromCatalog := installed, false
	if version == "" {
		fromCatalog = true
		for key, e := range c.Entries(ctx) {
			if e.Type != TypeApp {
				continue
			}
			if rest, ok := strings.CutPrefix(key, dep.Name+"/"); ok && !strings.Contains(rest, "/") {
				version = pickNewest(dep, version, rest)
			}
			if axis, ok := versionAxis(dep.Name, e); ok {
				for _, v := range e.PH[axis] {
					version = pickNewest(dep, version, v)
				}
			}
		}
	}
	if version == "" {
		return Resolved{}, false, nil
	}

	full := dep.Name + "/" + version
	res, found, err := c.lookupExact(ctx, full)
	if err != nil {
		return Resolved{}, false, err
	}
	if fromCatalog && !found {
		return Resolved{}, false, nil
	}
	res.Name, res.Installed = full, installed
	return res, true, nil
}

// solveOS resolves name as an os artifact: distro/name (bare) or distro/name/version
// (partial), against the #PH: axis of the template whose #TARGET: is distro/name/{axis}.
//
//   - have is checked first, as in solveApp.
//   - Only a template's declared version list is a candidate. An os entry's second
//     component names a different app, not a version of the first, so the distro's
//     other children are never considered.
//   - A flat, versionless entry ("ubuntu24/xfce4") is a lookupExact case, and a bare
//     distro ("ubuntu24") has no template to match.
func (c Catalog) solveOS(ctx context.Context, have Have, name string) (Resolved, bool, error) {
	parts := strings.SplitN(name, "/", 3)
	if len(parts) < 2 {
		return Resolved{}, false, nil
	}
	base := parts[0] + "/" + parts[1]
	versionQuery := ""
	if len(parts) == 3 {
		versionQuery = parts[2]
	}
	dep := Dep{Name: base, Version: versionQuery}

	installed := installedVersion(have, dep)
	version, fromCatalog := installed, false
	if version == "" {
		fromCatalog = true
		for _, e := range c.Entries(ctx) {
			if axis, ok := versionAxis(base, e); ok {
				for _, v := range e.PH[axis] {
					version = pickNewest(dep, version, v)
				}
			}
		}
	}
	if version == "" {
		return Resolved{}, false, nil
	}

	full := base + "/" + version
	res, found, err := c.lookupExact(ctx, full)
	if err != nil {
		return Resolved{}, false, err
	}
	if fromCatalog && !found {
		return Resolved{}, false, nil
	}
	res.Name, res.Installed = full, installed
	return res, true, nil
}

// pickNewest returns candidate in place of best when it satisfies dep and
// outranks whatever best already holds.
func pickNewest(dep Dep, best, candidate string) string {
	if dep.Satisfies(candidate) && (best == "" || utils.CompareVersions(candidate, best) > 0) {
		return candidate
	}
	return best
}

// ShortForm reports the short form a person would type to name a concrete,
// installed name again: its bare form when it sits directly below distro,
// unchanged otherwise. The inverse of SolveName's own distro-prefix retry,
// so both read the same distro rather than one going through
// config.ResolvedDefaultDistro ambiently while the other honors a project's
// selected root.
func ShortForm(distro, name string) string {
	if distro == "" {
		return name
	}
	if bare, ok := strings.CutPrefix(name, distro+"/"); ok && bare != "" {
		return bare
	}
	return name
}

// HaveFrom adapts a map keyed by installed name/version — an overlay scan, an
// index — to Have: the versions present directly below name.
func HaveFrom[V any](installed map[string]V) Have {
	return func(name string) []string {
		prefix := name + "/"
		var out []string
		for key := range installed {
			version, ok := strings.CutPrefix(key, prefix)
			if !ok || version == "" || strings.Contains(version, "/") {
				continue
			}
			out = append(out, version)
		}
		return out
	}
}

// SolveInstalled resolves a raw name to something already on disk, reading no
// recipe source: an installed version always answers before the catalog does,
// so the catalog could only add candidates that are not installed. It returns
// the resolved name and the highest-priority copy of it; found is false when
// nothing installed answers. installed maps each installed name to its copies
// in search order, as an overlay scan returns it.
func SolveInstalled(ctx context.Context, installed map[string][]string, distro, raw string) (name, path string, found bool, err error) {
	res, ok, err := Catalog(nil).SolveName(ctx, HaveFrom(installed), distro, raw)
	if err != nil || !ok {
		return "", "", false, err
	}
	copies := installed[res.Name]
	if len(copies) == 0 {
		return "", "", false, nil
	}
	return res.Name, copies[0], true, nil
}
