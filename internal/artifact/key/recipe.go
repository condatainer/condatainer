// Recipe helpers convert inputs shared by the recipe-backed schemes into
// canonical values. Individual scheme files decide whether to include them.
package key

import (
	"sort"

	"github.com/condatainer/condatainer/catalog"
	"github.com/condatainer/condatainer/internal/artifact/meta"
)

// RecipeDigest returns the digest every recipe-backed V1 scheme includes.
//   - The preimage is the recipe with whole-line comments removed: what it does, without what only describes it.
//   - The header goes too, since each header item that belongs in a key has its own model field.
//   - So a #PH: menu #AUTOUPDATE: grew, a reworded #DESC: or a changed scheduler directive moves no key.
//   - The input is the recipe as stored at /.cnt/recipe, a template with its tokens intact. Placeholders carry which variant was built.
func RecipeDigest(recipe []byte) string {
	return Digest(catalog.StripComments(recipe))
}

// Placeholders converts selected #PH: values into canonical model fields sorted
// by name. All recipe-backed V1 schemes include them because they select the
// concrete variant built from a template.
func Placeholders(selected map[string]string) []PlaceholderValue {
	if len(selected) == 0 {
		return nil
	}
	out := make([]PlaceholderValue, 0, len(selected))
	for name, value := range selected {
		out = append(out, PlaceholderValue{Name: name, Value: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Env converts #ENV: contributions into model fields sorted by key, with {prefix} left unsubstituted.
//   - Every recipe-backed V1 scheme includes them.
//   - #ENV: lives in the header, which the recipe digest omits. Without these lines, editing GENOME_FASTA would move no key.
//   - #ENV: is how one recipe finds another's output, so that silence would propagate to everything built on top.
//   - The ## notes are dropped, so rewording one mints no new artifact.
//   - Values are kept as well as names, since a value change is a build-input change.
func Env(env []meta.EnvVar) []EnvValue {
	if len(env) == 0 {
		return nil
	}
	out := make([]EnvValue, 0, len(env))
	for _, e := range env {
		out = append(out, EnvValue{Key: e.Key, Value: e.Value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
