package lock

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/capsule"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
)

// MaxSourceBytes bounds any file read out of a lock. A vendored source is a
// recipe or a Conda export; anything at this size is not one, and a lock is
// untrusted input that must not be able to exhaust memory.
const MaxSourceBytes = 8 << 20

// Entry is one verified artifact directory in cnt-lock/provenance/.
type Entry struct {
	// Path is the artifact directory relative to the lock directory.
	Path     string
	Manifest meta.Manifest
	// Identity and Equiv were regenerated from the vendored sources, never
	// taken on trust from the manifest.
	Identity meta.KeyRef
	Equiv    meta.KeyRef
	// Archs are the #ARCH: values of the vendored recipe, empty for an entry with
	// no recipe or one that declares none.
	Archs []catalog.Arch
}

// Verified is a checkout's artifact closure, keyed by relative artifact path.
type Verified struct {
	Entries map[string]*Entry
	// Roots are the artifact paths pins point at, sorted.
	Roots []string
	// Reachable is every entry the roots reach through dependency edges. An
	// entry outside it is unreferenced and may be pruned.
	Reachable map[string]bool
}

// reasonUnreachable is the problem for a vendored entry no pin leads to. Publish
// prunes such entries, so it does not stop a lock being written.
const reasonUnreachable = "overlay directory is not reachable from any pin"

// Problem is one validation failure, attributed to what it is about.
type Problem struct {
	// Artifact is the relative artifact path, empty for a project-level issue.
	Artifact string
	// Request is the pin key, empty when the problem is not about one.
	Request string
	Reason  string
	// Gap marks a pin that lacks an architecture's artifact. Pinning on that
	// architecture resolves it, so it never stops a pin being written.
	Gap bool
}

func (p Problem) String() string {
	switch {
	case p.Artifact != "" && p.Request != "":
		return fmt.Sprintf("%s (%s): %s", p.Request, p.Artifact, p.Reason)
	case p.Artifact != "":
		return fmt.Sprintf("%s: %s", p.Artifact, p.Reason)
	case p.Request != "":
		return fmt.Sprintf("%s: %s", p.Request, p.Reason)
	}
	return p.Reason
}

// Verify validates a lock against the vendored artifacts beside it, using only the checkout: no installed overlay, store, catalog, configuration, network or payload.
//   - Whether an artifact is available to run is project.Resolve's question.
//   - Problems are collected, not returned at the first failure.
func Verify(root string, l *Lock) (*Verified, []Problem) {
	dir := Dir(root)
	out := &Verified{Entries: map[string]*Entry{}}
	var problems []Problem

	present, err := listEntryDirs(root)
	if err != nil {
		return out, []Problem{{Reason: err.Error()}}
	}
	for _, relative := range present {
		entry, entryProblems := readEntry(dir, relative)
		problems = append(problems, entryProblems...)
		if entry != nil {
			out.Entries[relative] = entry
		}
	}

	// Pins must point at something that verified, built for the architecture
	// they are keyed under.
	for _, request := range l.Requests() {
		for _, arch := range slices.Sorted(maps.Keys(l.Pins[request].Artifacts)) {
			artifact := l.Pins[request].Artifacts[arch]
			entry, ok := out.Entries[artifact]
			if !ok {
				problems = append(problems, Problem{Request: request, Artifact: artifact,
					Reason: "pin points at a missing or invalid overlay directory"})
				continue
			}
			if reason := satisfies(request, entry.Manifest.Name); reason != "" {
				problems = append(problems, Problem{Request: request, Artifact: artifact, Reason: reason})
			}
			if recorded := entry.Manifest.Platform.Arch; recorded != arch {
				problems = append(problems, Problem{Request: request, Artifact: artifact,
					Reason: fmt.Sprintf("pinned for %s but built for %s", arch, recorded)})
			}
		}
	}
	out.Roots = sortedKeys(l.PinnedArtifacts())

	// Walk dependency edges from the roots. Reachability is the closure, not the
	// pin set: pruning against the roots alone would delete the closure.
	var edgeProblems []Problem
	out.Reachable, edgeProblems = reach(out.Entries, out.Roots)
	problems = append(problems, edgeProblems...)

	// Every architecture the lock covers needs an answer for every pin.
	for _, arch := range l.Archs {
		for _, gap := range out.Select(l, arch).Gaps {
			if !gap.Excluded {
				problems = append(problems, Problem{Request: gap.Request, Gap: true, Reason: gap.Reason})
			}
		}
	}

	for _, relative := range present {
		if !out.Reachable[relative] {
			problems = append(problems, Problem{Artifact: relative,
				Reason: reasonUnreachable})
		}
	}

	// A remote must name an artifact that is actually here.
	for _, artifact := range sortedKeys(remoteKeys(l)) {
		if _, ok := out.Entries[artifact]; !ok {
			problems = append(problems, Problem{Artifact: artifact,
				Reason: "remote refers to an overlay that is not vendored"})
		}
	}

	sort.Slice(problems, func(i, j int) bool { return problems[i].String() < problems[j].String() })
	return out, problems
}

// WithoutGaps drops the problems about another architecture's missing artifact.
// A caller working for one machine asks Select about that machine instead.
func WithoutGaps(problems []Problem) []Problem {
	return slices.DeleteFunc(slices.Clone(problems), func(p Problem) bool { return p.Gap })
}

// reach walks dependency edges from roots and returns every entry they reach,
// with the problem each edge that cannot be followed raises.
func reach(entries map[string]*Entry, roots []string) (map[string]bool, []Problem) {
	reachable := map[string]bool{}
	var problems []Problem
	var walk func(artifact string)
	walk = func(artifact string) {
		if reachable[artifact] {
			return
		}
		reachable[artifact] = true
		entry, ok := entries[artifact]
		if !ok {
			return
		}
		for _, dep := range entry.Manifest.Dependencies {
			child, reason := resolveEdge(entries, dep)
			if reason != "" {
				problems = append(problems, Problem{Artifact: artifact, Reason: reason})
				continue
			}
			walk(child)
		}
	}
	for _, artifact := range roots {
		walk(artifact)
	}
	return reachable, problems
}

// Gap is a pin with no artifact for an architecture.
type Gap struct {
	Request string
	// Excluded reports that the pin's recipe does not build on the architecture,
	// so nothing is missing that pinning there could supply.
	Excluded bool
	Reason   string
}

func (g Gap) String() string {
	if g.Request == "" {
		return g.Reason
	}
	return g.Request + ": " + g.Reason
}

// Selection is the artifact each request resolves to on one architecture.
type Selection struct {
	Arch string
	// Artifacts maps a request to its artifact path.
	Artifacts map[string]string
	// Gaps are the requests with no artifact, sorted. A lock that does not cover
	// the architecture at all reports one gap for the whole lock.
	Gaps []Gap
}

// Select resolves every pin for arch: the artifact built for it, else the noarch one.
func (v *Verified) Select(l *Lock, arch string) Selection {
	selection := Selection{Arch: arch, Artifacts: map[string]string{}}
	var missing []string
	for _, request := range l.Requests() {
		if artifact, ok := l.Pins[request].For(arch); ok {
			selection.Artifacts[request] = artifact
			continue
		}
		missing = append(missing, request)
	}
	covered := slices.Contains(l.Archs, arch)
	for _, request := range missing {
		if v.excludes(l.Pins[request], arch) {
			allowed, _ := v.recipeArchs(l.Pins[request])
			selection.Gaps = append(selection.Gaps, Gap{Request: request, Excluded: true,
				Reason: fmt.Sprintf("builds on %s only; this machine is %s", joinArchs(allowed), arch)})
			continue
		}
		if !covered {
			continue
		}
		selection.Gaps = append(selection.Gaps, Gap{Request: request,
			Reason: fmt.Sprintf("has no %s artifact; run `condatainer project lock` on an %s machine", arch, arch)})
	}
	if !covered && len(selection.Gaps) < len(missing) {
		selection.Gaps = append(selection.Gaps, Gap{Reason: fmt.Sprintf(
			"this lock does not cover %s; run `condatainer project lock` on an %s machine to add it", arch, arch)})
	}
	return selection
}

// recipeArchs reports the #ARCH: list of a pin's vendored recipe, and whether it has one.
func (v *Verified) recipeArchs(pin PinEntry) ([]catalog.Arch, bool) {
	for _, artifact := range pin.Paths() {
		if entry, ok := v.Entries[artifact]; ok && len(entry.Archs) > 0 {
			return entry.Archs, true
		}
	}
	return nil, false
}

// excludes reports that the pin's recipe lists architectures and not arch.
func (v *Verified) excludes(pin PinEntry, arch string) bool {
	allowed, listed := v.recipeArchs(pin)
	return listed && !slices.Contains(allowed, catalog.Arch(arch))
}

func joinArchs(archs []catalog.Arch) string {
	out := make([]string, len(archs))
	for i, arch := range archs {
		out[i] = string(arch)
	}
	return strings.Join(out, " ")
}

// Scope narrows a verification to what one selection reaches: its roots and
// their closure. Entries stay whole, so an artifact of another architecture is
// present but not reachable.
func (v *Verified) Scope(selection Selection) *Verified {
	roots := sortedKeys(setOf(maps.Values(selection.Artifacts)))
	reachable, _ := reach(v.Entries, roots)
	return &Verified{Entries: v.Entries, Roots: roots, Reachable: reachable}
}

func setOf(values func(yield func(string) bool)) map[string]bool {
	out := map[string]bool{}
	for value := range values {
		out[value] = true
	}
	return out
}

// resolveEdge finds the entry a dependency edge points at, by name and complete
// identity. An edge whose provenance was never recorded cannot be followed, and
// a lock that contains one is not a rebuild specification.
func resolveEdge(entries map[string]*Entry, dep meta.Dependency) (string, string) {
	if dep.Identity.Empty() || dep.Equiv.Empty() {
		return "", fmt.Sprintf("dependency %s carries no complete keys, so its provenance cannot be followed", dep.Name)
	}
	want := EntryPath(capsule.EntryName(dep.Name, dep.Identity.Digest()))
	entry, ok := entries[want]
	if !ok {
		return "", fmt.Sprintf("dependency %s@%s is not vendored at %s", dep.Name, short(dep.Identity.SHA256), want)
	}
	if entry.Manifest.Name != dep.Name {
		return "", fmt.Sprintf("dependency edge names %s but %s records %s", dep.Name, want, entry.Manifest.Name)
	}
	if entry.Identity != dep.Identity {
		return "", fmt.Sprintf("dependency %s expects identity %s but %s regenerates %s",
			dep.Name, short(dep.Identity.SHA256), want, short(entry.Identity.SHA256))
	}
	if entry.Equiv != dep.Equiv {
		return "", fmt.Sprintf("dependency %s expects equivalence %s but %s regenerates %s",
			dep.Name, short(dep.Equiv.SHA256), want, short(entry.Equiv.SHA256))
	}
	return want, ""
}

// ReadEntry reads and verifies one vendored artifact directory, regenerating its keys from the bytes rather than trusting the manifest.
//   - It uses capsule.ReadRecord, the code that reads an entry out of an image, with a bounded reader, so a lock and an image cannot disagree.
//   - It is exported for the upstream-endpoint walk, which runs before the lock is published.
func ReadEntry(root, relative string) (*Entry, error) {
	entry, problems := readEntry(Dir(root), relative)
	if entry == nil {
		if len(problems) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrInvalid, problems[0].Reason)
		}
		return nil, fmt.Errorf("%w: %s is unreadable", ErrInvalid, relative)
	}
	return entry, nil
}

func readEntry(lockDir, relative string) (*Entry, []Problem) {
	fail := func(format string, args ...any) []Problem {
		return []Problem{{Artifact: relative, Reason: fmt.Sprintf(format, args...)}}
	}

	record, err := capsule.ReadRecord(filepath.Join(lockDir, relative), readBounded)
	if err != nil {
		return nil, fail("%v", err)
	}
	// The directory name is addressing, never identity: the complete key is
	// regenerated and the name is checked against it.
	if err := capsule.CheckDirName(path.Base(relative), record); err != nil {
		return nil, fail("%v", err)
	}

	return &Entry{Path: relative, Manifest: record.Manifest,
		Identity: record.Derived.Identity.Ref, Equiv: record.Derived.Equiv.Ref,
		Archs: recipeArchs(record.Sources)}, nil
}

// recipeArchs reads #ARCH: out of the vendored recipe, if the entry has one.
func recipeArchs(sources map[string][]byte) []catalog.Arch {
	text, ok := sources[meta.RecipeFileName]
	if !ok {
		return nil
	}
	recipe, err := catalog.ParseRecipe(meta.RecipeFileName, bytes.NewReader(text))
	if err != nil {
		return nil
	}
	return recipe.Arch
}

// Sources reads one verified entry's vendored build inputs, keyed as
// manifest.source.files names them. A rebuild starts from exactly these bytes.
func Sources(root string, entry *Entry) (map[string][]byte, error) {
	sources, err := capsule.ReadSources(filepath.Join(Dir(root), entry.Path), entry.Manifest, readBounded)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", entry.Path, err)
	}
	return sources, nil
}

// readBounded reads a lock file, refusing a symlink and anything oversized.
// A vendored source is untrusted input: it must not escape the checkout through
// a link and must not be able to exhaust memory.
func readBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Size() > MaxSourceBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d limit", filepath.Base(path), info.Size(), MaxSourceBytes)
	}
	return os.ReadFile(path)
}

// listEntryDirs returns every provenance entry directory present, relative to
// the lock directory and sorted. A non-directory or symlink is refused rather
// than skipped: it is in the tracked tree and something put it there.
func listEntryDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(ProvenancePath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s/%s is a symlink", ProvenanceDir, entry.Name())
		}
		// A dotted directory is a write in progress, not an entry: skipping it
		// keeps an interrupted write invisible instead of a spurious problem.
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		out = append(out, EntryPath(entry.Name()))
	}
	sort.Strings(out)
	return out, nil
}

// satisfies reports why a concrete artifact name does not answer a request, or
// "". A request names one exact artifact, so the two names must be equal.
func satisfies(request, name string) string {
	if strings.HasPrefix(request, PathPrefix) {
		return ""
	}
	// The reserved base key addresses the project's root by slot, not by name,
	// so it never has to satisfy the name a #DEP: would render.
	if request == BaseKey {
		return ""
	}
	dep, err := parseRequest(request)
	if err != nil {
		return fmt.Sprintf("pin key is not a usable dependency: %v", err)
	}
	if reason := ConstraintReason(dep, request); reason != "" {
		return reason
	}
	if dep.NameVersion() != name {
		return fmt.Sprintf("request names %s but the overlay is %s", dep.NameVersion(), name)
	}
	return ""
}

func remoteKeys(l *Lock) map[string]bool {
	out := make(map[string]bool, len(l.Remotes))
	for artifact := range l.Remotes {
		out[artifact] = true
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func short(sha string) string {
	if len(sha) > capsule.IdentityChars {
		return sha[:capsule.IdentityChars]
	}
	return sha
}
