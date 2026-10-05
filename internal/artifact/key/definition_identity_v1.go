package key

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/catalog"
)

// deriveDefinitionIdentityV1 answers: "Which exact definition build is this?"
//
// Preimage, in canonical order:
//   - artifact type
//   - every #ENV value
//   - comment-stripped definition digest
//   - resolved upstream image digest, when an upstream exists
//   - every selected placeholder
//
// Definitions cannot have dependencies. The written upstream reference already reaches the key through the recipe, and From pins the bytes it resolved to. A failed resolution contributes the explicit "unrecorded" marker.
func deriveDefinitionIdentityV1(a Artifact) (Value, Model, error) {
	if a.Type != catalog.TypeOS {
		return Value{}, Model{}, fmt.Errorf("%s cannot derive type %s", DefinitionIdentityV1, a.Type)
	}
	if len(a.Deps) > 0 {
		return Value{}, Model{}, fmt.Errorf("%s cannot have dependencies", DefinitionIdentityV1)
	}

	model := Model{
		Kind:         KindIdentity,
		Type:         a.Type,
		Env:          Env(a.Env),
		Recipe:       RecipeDigest(a.Recipe),
		From:         a.From,
		Placeholders: Placeholders(a.Placeholders),
	}
	derived, err := valueFromModel(DefinitionIdentityV1, model)
	return derived, model, err
}
