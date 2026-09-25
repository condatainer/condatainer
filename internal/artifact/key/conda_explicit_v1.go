package key

import (
	"fmt"

	"github.com/condatainer/condatainer/catalog"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// deriveCondaExplicitV1 answers: "Which exact Conda package set is installed?"
//   - The preimage is explicit.txt exactly as stored, byte for byte.
//   - It does no parsing, normalization, sorting or model encoding.
func deriveCondaExplicitV1(m meta.Manifest, explicit []byte) (Value, error) {
	if m.Type != catalog.TypeApp {
		return Value{}, fmt.Errorf("%s cannot derive type %s", CondaExplicitV1, m.Type)
	}
	if len(m.Dependencies) > 0 {
		return Value{}, fmt.Errorf("%s cannot have dependencies", CondaExplicitV1)
	}
	return value(CondaExplicitV1, explicit), nil
}
