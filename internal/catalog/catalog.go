// Package catalog reads recipe collections: what a source offers, what a recipe
// declares, and in what order a dependency graph has to be built.
//
// Every decision that follows is the caller's, so nothing here reads process
// state, prints, or knows what an overlay is.
package catalog

import (
	"strings"

	"github.com/condatainer/condatainer/internal/artifact"
)

// DeriveType reports the type of a recipe from its path and headers.
//   - A .def is always os; everything else takes #TYPE: when declared, else data at two or more name components.
//   - A template counts the components of target, its module path.
//   - Only "app" and "data" are accepted.
func DeriveType(name, target string, isDef bool, declared string) artifact.Type {
	if isDef {
		return artifact.TypeOS
	}
	switch artifact.Type(declared) {
	case artifact.TypeApp, artifact.TypeData:
		return artifact.Type(declared)
	}
	path := target
	if path == "" {
		path = name
	}
	if strings.Count(path, "/") >= 2 {
		return artifact.TypeData
	}
	return artifact.TypeApp
}
