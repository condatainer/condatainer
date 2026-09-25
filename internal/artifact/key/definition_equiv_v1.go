package key

import (
	"fmt"

	"github.com/condatainer/condatainer/catalog"
)

// deriveDefinitionEquivV1 answers: "Can this definition-built artifact substitute for the requested artifact?"
//
// Preimage, in canonical order:
//   - artifact type
//   - every #ENV value
//   - comment-stripped definition digest
//   - every selected placeholder
//
// Definitions cannot have dependencies. The resolved upstream digest is omitted, so rebuilding the same definition after an upstream update changes identity but keeps equivalence.
func deriveDefinitionEquivV1(a Artifact) (Value, Model, error) {
	if a.Type != catalog.TypeOS {
		return Value{}, Model{}, fmt.Errorf("%s cannot derive type %s", DefinitionEquivV1, a.Type)
	}
	if len(a.Deps) > 0 {
		return Value{}, Model{}, fmt.Errorf("%s cannot have dependencies", DefinitionEquivV1)
	}

	model := Model{
		Kind:         KindEquiv,
		Type:         a.Type,
		Env:          Env(a.Env),
		Recipe:       RecipeDigest(a.Recipe),
		Placeholders: Placeholders(a.Placeholders),
	}
	derived, err := valueFromModel(DefinitionEquivV1, model)
	return derived, model, err
}
