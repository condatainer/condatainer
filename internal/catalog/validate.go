package catalog

import (
	"errors"
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/utils"
)

// ErrInvalidRecipe reports a recipe that declares something its type may not.
var ErrInvalidRecipe = errors.New("catalog: invalid recipe")

// ErrUnsupportedArch reports a recipe that does not build on this machine's architecture.
var ErrUnsupportedArch = errors.New("catalog: recipe does not build on this architecture")

// Validate reports headers a recipe of this type may not declare.
//   - It runs once the recipe is fetched for a build, not while indexing: one bad recipe must not take a whole collection out of a listing.
//   - The rules only a recipe has:
//   - #ARCH: only app and data, and only noarch or a list of amd64 and arm64: an os is a root
//     filesystem and always architecture-specific.
//   - A definition declares neither #SOURCE: nor #INPUT:. Apptainer builds it from its
//     own bootstrap, so nothing would fetch or read them.
//   - #PH:, #TARGET: and #SOURCE: placeholders agree.
//   - #REDISTRIBUTE: is rejected unless yes or no. A typo read as "unanswered" would
//     fall back to the type default and publish what the author meant to hold back.
func (r *Recipe) Validate() error {
	var errs []error

	for _, problem := range r.placeholderProblems {
		errs = append(errs, fmt.Errorf("%w: %s: %s", ErrInvalidRecipe, r.Name, problem))
	}

	if len(r.Arch) > 0 {
		switch r.Type {
		case artifact.TypeApp, artifact.TypeData:
			if problem := archProblem(r.Arch); problem != "" {
				errs = append(errs, fmt.Errorf("%w: %s declares #ARCH:%s; %s",
					ErrInvalidRecipe, r.Name, joinArch(r.Arch), problem))
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
		for _, a := range utils.ScanAnnotations(r.Text) {
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
	if r.DeclaredType != "" && artifact.Type(r.DeclaredType) != r.Type {
		if artifact.Type(r.DeclaredType) == artifact.TypeEnv {
			errs = append(errs, fmt.Errorf("%w: %s declares #TYPE:env; env is what `overlay freeze` produces from a writable overlay and cannot be declared by a recipe",
				ErrInvalidRecipe, r.Name))
		} else {
			errs = append(errs, fmt.Errorf("%w: %s declares #TYPE:%s but is %s; a recipe may declare %s or %s",
				ErrInvalidRecipe, r.Name, r.DeclaredType, r.Type, artifact.TypeApp, artifact.TypeData))
		}
	}

	// A source name becomes $CNT_BUILD_SRC_<name> and addresses one record in the
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

// archProblem says why a #ARCH: list is not valid, or "".
//   - The values are noarch alone, or amd64 and arm64 each at most once.
func archProblem(archs []Arch) string {
	seen := map[Arch]bool{}
	for _, a := range archs {
		switch a {
		case ArchAMD64, ArchARM64, ArchNoarch:
		default:
			return fmt.Sprintf("%s is not an architecture; the values are noarch, or amd64 and arm64", a)
		}
		if seen[a] {
			return fmt.Sprintf("%s is listed twice", a)
		}
		seen[a] = true
	}
	if seen[ArchNoarch] && len(archs) > 1 {
		return "noarch cannot be listed with architectures"
	}
	return ""
}
