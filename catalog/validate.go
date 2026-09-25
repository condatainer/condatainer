package catalog

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// ErrInvalidRecipe reports a recipe that declares something its type may not.
var ErrInvalidRecipe = errors.New("catalog: invalid recipe")

// IsPathDep reports whether a #DEP: value names an overlay file rather than a catalog name/version.
//   - It is exported because it decides which grammar a dependency is read with: Normalize and ParseDep apply to names only, and a second answer elsewhere would let one string be a name in one place and a path in another.
//   - The extensions are utils.IsOverlay's plus .sif; this package cannot import utils, so they are kept in sync by hand.
func IsPathDep(value string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(value))) {
	case ".sqf", ".sqsh", ".squashfs", ".img", ".ext3", ".sif":
		return true
	}
	return false
}

// ValidateDeps reports why a build of this type may not declare these dependencies, or nil.
//   - Recipe.Validate and build.FromExternalSource share it: an external build has no catalog recipe to validate.
//   - A running script is not held to it, since it mounts what it names and may name an overlay path.
//   - Only data may declare #DEP:. An app is prebuilt and self-contained, and an os
//     is self-contained by definition. A recipe that needs a compiler is an os
//     artifact providing one.
//   - A build dependency is a name/version, never a path. A built artifact records
//     each edge as a name plus a complete identity, and a path supplies neither. That
//     keeps a project's vendored source closure total.
func ValidateDeps(name string, typ Type, deps []string) error {
	if len(deps) == 0 {
		return nil
	}
	var errs []error

	if typ != TypeData {
		errs = append(errs, fmt.Errorf("%w: %s is type %s and may not declare #DEP: (%s); only data has build dependencies",
			ErrInvalidRecipe, name, typ, strings.Join(deps, ", ")))
	}

	var paths []string
	for _, dep := range deps {
		if IsPathDep(dep) {
			paths = append(paths, dep)
		}
	}
	if len(paths) > 0 {
		errs = append(errs, fmt.Errorf("%w: %s declares #DEP: %s; a build dependency must be a name/version, not an overlay path",
			ErrInvalidRecipe, name, strings.Join(paths, ", ")))
	}

	return errors.Join(errs...)
}

// HasComponents reports whether dep's slash-separated components occur as a contiguous
// run of name's. Components compare as exact strings, never semantically (2.7.11b and
// 2.7.11 name different builds), and never as substrings: star/2.7.11b must not match
// inside star2.7.11b-old. internal/artifact/key.Role uses it: an app or os dependency
// contributes to equivalence only when this is true, otherwise it is build history.
func HasComponents(name, dep string) bool {
	n := strings.Split(Normalize(name), "/")
	d := strings.Split(Normalize(dep), "/")
	if len(d) == 0 || len(d) > len(n) {
		return false
	}
	for i := 0; i+len(d) <= len(n); i++ {
		if slices.Equal(n[i:i+len(d)], d) {
			return true
		}
	}
	return false
}

// Validate reports headers a recipe of this type may not declare.
//   - It runs once the recipe is fetched for a build, not while indexing: one bad recipe must not take a whole collection out of a listing.
//   - The #DEP: rules are in ValidateDeps.
//   - The rules only a recipe has:
//   - #ARCH: only app and data, and only native or noarch: an os is a root filesystem
//     and always architecture-specific.
//   - A definition declares neither #SOURCE: nor #INPUT:. Apptainer builds it from its
//     own bootstrap, so nothing would fetch or read them.
//   - #PH:, #TARGET: and #SOURCE: placeholders agree.
//   - #REDISTRIBUTE: is rejected unless yes or no. A typo read as "unanswered" would
//     fall back to the type default and publish what the author meant to hold back.
func (r *Recipe) Validate() error {
	var errs []error

	if err := ValidateDeps(r.Name, r.Type, r.Deps); err != nil {
		errs = append(errs, err)
	}

	for _, problem := range r.placeholderProblems {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrInvalidRecipe, r.Name, problem))
	}

	if r.Arch != "" {
		switch r.Type {
		case TypeApp, TypeData:
			if r.Arch != ArchNative && r.Arch != ArchNoarch {
				errs = append(errs, fmt.Errorf("%w: %s declares #ARCH:%s; the values are %s and %s",
					ErrInvalidRecipe, r.Name, r.Arch, ArchNative, ArchNoarch))
			}
		default:
			errs = append(errs, fmt.Errorf("%w: %s is type %s and may not declare #ARCH:; a root filesystem is always architecture-specific",
				ErrInvalidRecipe, r.Name, r.Type))
		}
	}

	// #SOURCE: and #INPUT: belong to a script build, which fetches before its body
	// and reads answers on stdin. A definition has neither, so declaring one would
	// be silently ignored.
	if strings.HasSuffix(r.Path, ".def") {
		seen := map[string]bool{}
		for _, a := range ScanAnnotations(r.Text) {
			if (a.Key == "#SOURCE" || a.Key == "#INPUT") && !seen[a.Key] {
				seen[a.Key] = true
				errs = append(errs, fmt.Errorf("%w: %s is a definition and may not declare %s:; only a script recipe fetches sources or asks for input",
					ErrInvalidRecipe, r.Name, a.Key))
			}
		}
	}

	// A #TYPE: that disagrees with the derived type is rejected rather than
	// ignored, for the reason #REDISTRIBUTE: is: silence and a wrong answer must
	// not look the same. DeriveType accepts only app and data, and a .def takes
	// its type from its path, so anything else read as silence and built as
	// whatever the default happened to be. Restating the type a recipe already
	// has stays legal.
	//
	// #TYPE: env is the case this exists for. env is what `overlay freeze`
	// produces from a writable overlay; no recipe can produce one, and DeriveType
	// never returns it, so the declaration could only ever have built an app.
	if r.DeclaredType != "" && Type(r.DeclaredType) != r.Type {
		if Type(r.DeclaredType) == TypeEnv {
			errs = append(errs, fmt.Errorf("%w: %s declares #TYPE:env; env is what `overlay freeze` produces from a writable overlay and cannot be declared by a recipe",
				ErrInvalidRecipe, r.Name))
		} else {
			errs = append(errs, fmt.Errorf("%w: %s declares #TYPE:%s but is %s; a recipe may declare %s or %s",
				ErrInvalidRecipe, r.Name, r.DeclaredType, r.Type, TypeApp, TypeData))
		}
	}

	// A source name becomes $CNT_SRC_<name> and addresses one record in the
	// identity, so a malformed or repeated one is rejected rather than skipped:
	// the body would otherwise run against an unset variable.
	type sourceKey struct{ name, arch string }
	seenSource := make(map[sourceKey]bool, len(r.Sources))
	qualified := make(map[string]bool, len(r.Sources))
	plain := make(map[string]bool, len(r.Sources))
	for _, src := range r.Sources {
		key := sourceKey{src.Name, src.Arch}
		if seenSource[key] {
			errs = append(errs, fmt.Errorf("%w: %s declares #SOURCE:%s twice for the same architecture; one name is one input",
				ErrInvalidRecipe, r.Name, src.Name))
		}
		seenSource[key] = true
		if src.Arch == "" {
			plain[src.Name] = true
		} else {
			qualified[src.Name] = true
		}
	}
	for name := range qualified {
		if plain[name] {
			errs = append(errs, fmt.Errorf("%w: %s declares #SOURCE:%s both with and without an architecture; use one form",
				ErrInvalidRecipe, r.Name, name))
		}
	}
	for _, raw := range scanMalformedSources(r.Text) {
		errs = append(errs, fmt.Errorf("%w: %s declares #SOURCE:%s; the form is a name, an optional architecture (%s), then one URL, or ask: and a prompt; the name may hold only letters, digits and underscore",
			ErrInvalidRecipe, r.Name, raw, strings.Join(SourceArches, ", ")))
	}

	if r.Redistribute != "" && r.Redistribute != "yes" && r.Redistribute != "no" {
		errs = append(errs, fmt.Errorf("%w: %s declares #REDISTRIBUTE:%s; the values are yes and no",
			ErrInvalidRecipe, r.Name, r.Redistribute))
	}

	return errors.Join(errs...)
}
