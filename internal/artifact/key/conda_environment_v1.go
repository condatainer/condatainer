package key

import (
	"fmt"

	"github.com/condatainer/condatainer/catalog"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// deriveCondaEnvironmentV1 answers: "Can this Conda environment substitute for the requested environment?"
//   - The preimage is environment.yml exactly as stored, byte for byte.
//   - It does no parsing, normalization, sorting or model encoding.
func deriveCondaEnvironmentV1(m meta.Manifest, environment []byte) (Value, error) {
	if m.Type != catalog.TypeApp {
		return Value{}, fmt.Errorf("%s cannot derive type %s", CondaEnvironmentV1, m.Type)
	}
	if len(m.Dependencies) > 0 {
		return Value{}, fmt.Errorf("%s cannot have dependencies", CondaEnvironmentV1)
	}
	return value(CondaEnvironmentV1, environment), nil
}
