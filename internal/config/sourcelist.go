package config

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/condatainer/condatainer/internal/catalog"
)

// ErrSourceNotFound reports a source name a layer's list does not hold.
var ErrSourceNotFound = errors.New("no such source")

// Position is where a source goes in its layer's list. At most one field is set;
// none appends a new source and leaves an existing one where it is.
type Position struct {
	First, Last   bool
	Before, After string
}

// LayerSourceList reads the `sources` list of the config file at path, in order.
// A missing file or key is an empty list.
func LayerSourceList(path string) []catalog.Spec {
	l, err := readLayer(path, "")
	if err != nil {
		return nil
	}
	return decodeSourceList(l.raw("sources"))
}

// WriteLayerSourceList replaces the `sources` list of the config file at path,
// removing the key when list is empty.
func WriteLayerSourceList(path string, list []catalog.Spec) error {
	if len(list) == 0 {
		return DeleteConfigKey(path, "sources")
	}
	entries := make([]map[string]string, len(list))
	for i, s := range list {
		entries[i] = map[string]string{s.Name: s.Base}
	}
	return UpdateConfigKey(path, "sources", entries)
}

// PlaceSource puts spec into list at pos and returns the new list.
//   - A name already in list is replaced, and moved only when pos says where.
//   - Before or after a name list does not hold is ErrSourceNotFound.
func PlaceSource(list []catalog.Spec, spec catalog.Spec, pos Position) ([]catalog.Spec, error) {
	i := slices.IndexFunc(list, func(s catalog.Spec) bool { return s.Name == spec.Name })
	if i >= 0 && pos == (Position{}) {
		out := slices.Clone(list)
		out[i] = spec
		return out, nil
	}
	out := slices.DeleteFunc(slices.Clone(list), func(s catalog.Spec) bool { return s.Name == spec.Name })
	at := len(out)
	switch {
	case pos.First:
		at = 0
	case pos.Before != "", pos.After != "":
		other := cmp.Or(pos.Before, pos.After)
		j := slices.IndexFunc(out, func(s catalog.Spec) bool { return s.Name == other })
		if j < 0 {
			return nil, fmt.Errorf("%w: %s", ErrSourceNotFound, other)
		}
		at = j
		if pos.After != "" {
			at = j + 1
		}
	}
	return slices.Insert(out, at, spec), nil
}
