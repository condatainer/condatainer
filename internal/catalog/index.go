package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
)

// IndexSchemaVersion is the format of index/recipes.json and index/helpers.json.
const IndexSchemaVersion = 1

// ErrIndexSchema reports an index this build does not read.
var ErrIndexSchema = errors.New("catalog: unsupported index schema")

// DecodeIndex reads an index document `{"schema_version": N, "<member>": {...}}` and decodes member into into.
//   - The version is required and read first. A newer one says to update.
func DecodeIndex(data []byte, member string, into any) error {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("catalog: decode index: %w", err)
	}
	var version int
	if raw, ok := doc["schema_version"]; ok {
		if err := json.Unmarshal(raw, &version); err != nil {
			return fmt.Errorf("catalog: decode index: schema_version: %w", err)
		}
	}
	if version != IndexSchemaVersion {
		hint := ""
		if version > IndexSchemaVersion {
			hint = "; update CondaTainer"
		}
		return fmt.Errorf("%w: index is schema_version %d (this build reads %d)%s", ErrIndexSchema, version, IndexSchemaVersion, hint)
	}
	raw, ok := doc[member]
	if !ok {
		return fmt.Errorf("catalog: decode index: no %q", member)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("catalog: decode index: %w", err)
	}
	return nil
}
