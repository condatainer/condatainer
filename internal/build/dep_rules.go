package build

import (
	"errors"
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/utils"
)

// validateDeps reports why a build of this type may not declare these dependencies, or nil.
//   - validateRecipe and the external build share it: an external build has no catalog recipe to validate.
//   - A running script is not held to it, since it mounts what it names and may name an overlay path.
//   - Only data may declare #DEP:. An app is prebuilt and self-contained, and an os
//     is self-contained by definition. A recipe that needs a compiler is an os
//     artifact providing one.
//   - A build dependency is a name/version, never a path. A built artifact records
//     each edge as a name plus a complete identity, and a path supplies neither. That
//     keeps a project's vendored source closure total.
func validateDeps(name string, typ artifact.Type, deps []string) error {
	if len(deps) == 0 {
		return nil
	}
	var errs []error

	if typ != artifact.TypeData {
		errs = append(errs, fmt.Errorf("%w: %s is type %s and may not declare #DEP: (%s); only data has build dependencies",
			catalog.ErrInvalidRecipe, name, typ, strings.Join(deps, ", ")))
	}

	var paths []string
	for _, dep := range deps {
		if utils.IsPathDep(dep) {
			paths = append(paths, dep)
		}
	}
	if len(paths) > 0 {
		errs = append(errs, fmt.Errorf("%w: %s declares #DEP: %s; a build dependency must be a name/version, not an overlay path",
			catalog.ErrInvalidRecipe, name, strings.Join(paths, ", ")))
	}

	return errors.Join(errs...)
}

// validateRecipe reports why a catalog recipe may not be built: the dependency rules, then the headers its type may not declare.
func validateRecipe(r *catalog.Recipe) error {
	return errors.Join(validateDeps(r.Name, r.Type, r.Deps), r.Validate())
}
