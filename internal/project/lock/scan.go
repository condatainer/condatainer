package lock

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/utils"
)

// Kind is what a declaration addresses.
type Kind string

const (
	// KindName addresses an artifact by name, optionally constrained.
	KindName Kind = "name"
	// KindPath addresses an immutable overlay by project-relative path.
	KindPath Kind = "path"
	// KindWritable addresses a writable .img, which has no identity to pin.
	KindWritable Kind = "writable"
	// KindExternal addresses a .sqf outside the project — absolute, or reached
	// by climbing out of the root.
	KindExternal Kind = "external"
)

// Pinnable reports whether an artifact can be pinned for this kind.
//
//   - A name is pinnable: the store resolves it by identity.
//   - A project-relative .sqf is pinnable: restore owns that path.
//   - A writable .img has no identity to pin.
//   - An external .sqf is someone else's file, which restore must never write to.
func (k Kind) Pinnable() bool { return k == KindName || k == KindPath }

// Request is one declaration a project makes, merged across every script that
// makes it.
type Request struct {
	// Key is the canonical pin key: catalog.Dep.String() for a name, and
	// PathPrefix plus the cleaned relative path for a path.
	Key  string
	Kind Kind
	// Dep is the parsed declaration, set for KindName only.
	Dep catalog.Dep
	// Path is the cleaned project-relative path, set for the path kinds.
	Path string
	// Scripts are the project-relative scripts that declared this, sorted.
	// Displayed, never serialized: rescanning finds them again.
	Scripts []string
	// first locates the declaration this request was created from, so a problem
	// only decidable after every script has been read still points at a line.
	first Finding
}

// PathCandidates lists the pin keys a KindPath request might mean, in preference order: the literal root-relative key, then the same suffix relative to each declaring script's directory.
//   - A script can name a file beside it without spelling out its folder.
//   - Every other kind returns its own key alone.
func (r Request) PathCandidates() []string {
	if r.Kind != KindPath {
		return []string{r.Key}
	}
	candidates := []string{r.Key}
	seen := map[string]bool{r.Key: true}
	for _, script := range r.Scripts {
		dir := path.Dir(script)
		if dir == "." {
			continue
		}
		key := PathPrefix + path.Clean(path.Join(dir, r.Path))
		if !seen[key] {
			seen[key] = true
			candidates = append(candidates, key)
		}
	}
	return candidates
}

// ConstraintReason reports why a version constraint cannot appear in a project
// declaration, or "".
//
//   - A range is a build-recipe feature. A result should name the exact version it used.
//   - A range would give one module two keys, and so two possible pins.
func ConstraintReason(dep catalog.Dep, declaration string) string {
	if dep.Op == "" {
		return ""
	}
	return fmt.Sprintf("%s carries a version constraint, which only a build recipe may use; declare %s exactly",
		declaration, dep.NameVersion())
}

// unpinnableReason explains why a declaration cannot be pinned and what closes
// it. Kind-specific, because the two are unpinnable for different reasons and
// the reader can only act on the one that applies.
func unpinnableReason(kind Kind, target string) string {
	switch kind {
	case KindWritable:
		return fmt.Sprintf("%s is writable, so it has no identity to pin; freeze it into the project with `condatainer overlay freeze %s overlays/<name>.sqf` and declare that instead",
			target, target)
	default:
		// A `../` declaration is the case worth spelling out: both readings
		// were tried and both escape.
		anchor := ""
		if !path.IsAbs(target) {
			anchor = " (tried relative to the project root and relative to each script declaring it)"
		}
		return fmt.Sprintf("%s is outside the project%s, so restore cannot own that path; copy it under the project and declare that path instead",
			target, anchor)
	}
}

// Finding is a declaration problem the scanner can see without leaving the
// checkout. It is reported rather than raised so the caller decides severity:
// `project lock` lists them, `project validate` fails on them.
type Finding struct {
	Script string
	Line   int
	Text   string
	Reason string
}

// ScanResult is what a project declares.
type ScanResult struct {
	// Requests are unique declarations, sorted by key.
	Requests []Request
	// Scripts are every scanned file, project-relative and sorted.
	Scripts  []string
	Findings []Finding
}

// ScanScript reads one script's declarations, merged and finalized exactly as Scan does for a whole project.
//   - Execution reads a script through this rather than through its own parse, so a run and a lock cannot disagree about what the script declares — which is the only reason a lock means anything at run time.
//   - The script must be inside root.
func ScanScript(root, script string) (*ScanResult, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	script, err = filepath.Abs(script)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, script)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s is not inside %s", script, root)
	}
	result := &ScanResult{}
	merged := map[string]*Request{}
	scanned, err := scanScript(script, filepath.ToSlash(rel), merged, result)
	if err != nil {
		return nil, err
	}
	if scanned {
		result.Scripts = append(result.Scripts, filepath.ToSlash(rel))
	}
	finalize(merged, result)
	return result, nil
}

// ScanOptions tunes discovery.
type ScanOptions struct {
	// ExcludeDirs are additional directory names to skip anywhere in the tree.
	// cnt-lock and every dot-directory are always skipped.
	ExcludeDirs []string
}

// Scan walks a project root and returns what its scripts declare.
//
// A declaration counts wherever it is written; position carries no meaning, so
// the scanner and the runtime read a script the same way.
func Scan(root string, opts ScanOptions) (*ScanResult, error) {
	skip := map[string]bool{DirName: true}
	for _, dir := range opts.ExcludeDirs {
		if dir = strings.TrimSpace(dir); dir != "" {
			skip[dir] = true
		}
	}

	merged := map[string]*Request{}
	result := &ScanResult{}

	walk := func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			// A dot-directory is tool state, not project source: .git, .venv,
			// .snakemake, .tox and a local conda env all carry shell scripts of
			// their own, and a #DEP: in one of those is not this project's
			// declaration. The root itself is read even when it is hidden.
			if path != root && (skip[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		// A directory symlink is never followed, and a symlinked script is not
		// read: either can point outside the checkout, and a lock describes the
		// checkout.
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if !isShellScript(entry.Name()) {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		scanned, err := scanScript(path, relSlash, merged, result)
		if scanned {
			result.Scripts = append(result.Scripts, relSlash)
		}
		return err
	}

	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}

	finalize(merged, result)
	return result, nil
}

// finalize turns merged declarations into a sorted result and reports any
// unpinnable one left undeclared.
//
// The marker merges across scripts, so whether one is missing is decidable only
// once every script in scope has been read — which is why this runs here rather
// than as each declaration is parsed.
func finalize(merged map[string]*Request, result *ScanResult) {
	taken := make(map[string]bool, len(merged))
	for key := range merged {
		taken[key] = true
	}
	for _, request := range merged {
		sort.Strings(request.Scripts)
		reclassifyEscaped(request, taken)
	}
	for _, request := range merged {
		if !request.Kind.Pinnable() {
			finding := request.first
			finding.Reason = unpinnableReason(request.Kind, request.Path)
			result.Findings = append(result.Findings, finding)
		}
		result.Requests = append(result.Requests, *request)
	}
	sort.Slice(result.Requests, func(i, j int) bool { return result.Requests[i].Key < result.Requests[j].Key })
	sort.Strings(result.Scripts)
	sort.Slice(result.Findings, func(i, j int) bool {
		if result.Findings[i].Script != result.Findings[j].Script {
			return result.Findings[i].Script < result.Findings[j].Script
		}
		return result.Findings[i].Line < result.Findings[j].Line
	})
}

// reclassifyEscaped promotes a KindExternal request to KindPath when the same
// suffix, taken relative to one of its declaring scripts, stays inside the project.
//
//   - A `../` declaration escapes only under the root anchor.
//   - An absolute declaration is left alone: it names a location on disk.
//   - taken holds the keys already in use, so two declarations never share one key.
//   - A collision skips to the next script, or leaves the request KindExternal.
func reclassifyEscaped(request *Request, taken map[string]bool) {
	if request.Kind != KindExternal || path.IsAbs(request.Path) {
		return
	}
	for _, script := range request.Scripts {
		dir := path.Dir(script)
		if dir == "." {
			continue
		}
		joined := path.Clean(path.Join(dir, request.Path))
		if joined == ".." || strings.HasPrefix(joined, "../") {
			continue
		}
		key := PathPrefix + joined
		if taken[key] {
			continue
		}
		request.Kind = KindPath
		request.Path = joined
		request.Key = key
		return
	}
}

// isShellScript reports whether a file should be read for declarations.
// Extension only: `run` executes every project script with /bin/bash.
func isShellScript(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".sh", ".bash":
		return true
	}
	return false
}

// scanScript reads one script's declarations, reporting whether it was read at
// all: a build recipe is skipped and contributes nothing.
func scanScript(path, rel string, merged map[string]*Request, result *ScanResult) (bool, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if isBuildRecipe(text) {
		return false, nil
	}

	for _, annotation := range catalog.Select(catalog.ScanAnnotations(text), "#DEP") {
		if annotation.Value == "" {
			continue
		}
		request, reason := ParseDeclaration(annotation.Value)
		if reason != "" {
			result.Findings = append(result.Findings, Finding{
				Script: rel, Line: annotation.Line, Text: annotation.Value, Reason: reason})
			continue
		}
		have, ok := merged[request.Key]
		if !ok {
			request.Scripts = []string{rel}
			request.first = Finding{Script: rel, Line: annotation.Line, Text: annotation.Value}
			merged[request.Key] = &request
			continue
		}
		if have.Scripts[len(have.Scripts)-1] != rel {
			have.Scripts = append(have.Scripts, rel)
		}
	}
	return true, nil
}

// isBuildRecipe reports whether a script builds an artifact rather than analysing with one.
//   - Only a recipe expands $CNT_PREFIX, where a build writes its payload.
//   - A recipe's #DEP: are build dependencies already in that artifact's provenance, so reading them would make the project pin what it never mounts.
//   - Comments are stripped first, and both $CNT_PREFIX and ${CNT_PREFIX} count.
func isBuildRecipe(text []byte) bool {
	stripped := string(catalog.StripComments(text))
	return strings.Contains(stripped, "$CNT_PREFIX") || strings.Contains(stripped, "${CNT_PREFIX}")
}

// ParseDeclaration turns one declaration into a Request, or returns why it cannot be one.
//   - The reason is empty exactly when the request is usable.
//   - It is the grammar of `#DEP:` and `exec -o`, so a name classifies the same on the command line and in a script.
func ParseDeclaration(value string) (Request, string) {
	if utils.IsOverlay(value) || utils.IsSif(value) {
		clean := filepath.ToSlash(filepath.Clean(value))
		kind := KindPath
		switch {
		case utils.IsImg(value):
			kind = KindWritable
		case path.IsAbs(clean), clean == "..", strings.HasPrefix(clean, "../"):
			// Outside the project, so restore has no path it may write.
			kind = KindExternal
		}
		return Request{Key: PathPrefix + clean, Kind: kind, Path: clean}, ""
	}

	dep, err := catalog.ParseDep(value)
	if err != nil {
		return Request{}, "not a usable dependency: " + err.Error()
	}
	if why := ConstraintReason(dep, value); why != "" {
		return Request{}, why
	}
	return Request{Key: dep.String(), Kind: KindName, Dep: dep}, ""
}

// parseRequest turns a canonical pin key back into the dependency it
// renders. It is Request.Key's inverse for a named request, and the one place
// that knows a key is not simply a name.
func parseRequest(key string) (catalog.Dep, error) {
	if path, ok := strings.CutPrefix(key, PathPrefix); ok {
		return catalog.Dep{}, fmt.Errorf("%q addresses a path, not a name", path)
	}
	return catalog.ParseDep(key)
}
