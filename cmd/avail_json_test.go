package cmd

import (
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/catalog"
)

// A template is one record holding every value of each placeholder, and with
// --expand its variants replace it, each with the one value it was expanded
// with and its dependencies filled in.
func TestAvailDocListsTemplatesAndVariants(t *testing.T) {
	entry := &catalog.Entry{Type: artifact.TypeData, Deps: []string{"star/{star_version}"}, Arch: []catalog.Arch{"amd64"}}
	template := PackageInfo{
		Name: "grch38/star", Source: "cnt", Path: "recipes/star.sh", IsTemplate: true, Entry: entry,
		TargetTemplate: "grch38/star/{star_version}", PHNames: []string{"star_version"},
		PH: map[string][]string{"star_version": {"2.7.11b", "2.7.10a"}},
	}
	variant := PackageInfo{
		Name: "grch38/star/2.7.11b", Source: "cnt", Path: "recipes/star.sh", IsInstalled: true, Entry: entry,
		TargetTemplate: "grch38/star/{star_version}", PH: map[string][]string{"star_version": {"2.7.11b"}},
	}
	noAlias := func(string) string { return "" }

	defer func(old bool) { availExpand = old }(availExpand)
	availExpand = false
	doc := availDoc([]PackageInfo{template, variant}, noAlias)
	if len(doc.Recipes) != 2 {
		t.Fatalf("recipes = %+v", doc.Recipes)
	}
	got := doc.Recipes[0]
	if got.Template == nil || !slices.Equal(got.Template.Values["star_version"], []string{"2.7.11b", "2.7.10a"}) ||
		got.Template.Placeholders != nil || got.Type != artifact.TypeData || got.Source != "cnt" ||
		got.Recipe != "recipes/star.sh" || !slices.Equal(got.BuildsOn, []string{"amd64"}) || !got.Buildable {
		t.Errorf("template = %+v", got)
	}

	availExpand = true
	doc = availDoc([]PackageInfo{template, variant}, noAlias)
	if len(doc.Recipes) != 1 {
		t.Fatalf("expanded recipes = %+v", doc.Recipes)
	}
	got = doc.Recipes[0]
	if got.Name != "grch38/star/2.7.11b" || !got.Installed || got.Template.Placeholders["star_version"] != "2.7.11b" ||
		got.Template.Values != nil || !slices.Equal(got.Deps, []string{"star/2.7.11b"}) {
		t.Errorf("variant = %+v", got)
	}
	if entry.Deps[0] != "star/{star_version}" {
		t.Error("filling in a variant changed the recipe's own dependencies")
	}
}
