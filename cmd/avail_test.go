package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/catalog"
)

func TestFormatPackageLinePutsDescriptionOnSecondLine(t *testing.T) {
	pkg := PackageInfo{
		Name:        "ubuntu24/simple-os",
		IsContainer: true,
		Source:      "test",
		Description: "Small OS overlay providing jq",
	}

	got := formatPackageLine(pkg, true)
	if !strings.Contains(got, "\n  "+pkg.Description) {
		t.Fatalf("formatPackageLine = %q, want an indented description line", got)
	}
	if strings.Contains(strings.SplitN(got, "\n", 2)[0], pkg.Description) {
		t.Fatalf("description remained on the metadata line: %q", got)
	}
}

func TestFormatPackageLineCanHideDescription(t *testing.T) {
	pkg := PackageInfo{Name: "hello/1.0", Description: "distinct description"}
	if got := formatPackageLine(pkg, false); strings.Contains(got, pkg.Description) {
		t.Fatalf("formatPackageLine = %q, want no description", got)
	}
}

func TestFormatDescriptionWrapsAndIndents(t *testing.T) {
	got := formatDescription("one two three four", 2, 12)
	want := "  one two\n  three four"
	if got != want {
		t.Fatalf("formatDescription = %q, want %q", got, want)
	}
}

func TestFormatListDescriptionUsesSecondLine(t *testing.T) {
	got := formatListDescription("hello/1.0", "one two three four", 13)
	want := "hello/1.0\n  one two\n  three four"
	if got != want {
		t.Fatalf("formatListDescription = %q, want %q", got, want)
	}
}

// A recipe this machine cannot build is listed with the architectures it needs.
func TestAvailMarksARecipeThisMachineCannotBuild(t *testing.T) {
	plain := formatPackageLine(PackageInfo{Name: "cellranger/9.0.1", NeedsArch: "amd64"}, false)
	if !strings.Contains(plain, "needs amd64") {
		t.Errorf("plain line = %q, want it to say the recipe needs amd64", plain)
	}
	template := formatTemplateLine(PackageInfo{Name: "cellranger", IsTemplate: true, NeedsArch: "amd64"}, false)
	if !strings.Contains(template, "needs amd64") {
		t.Errorf("template line = %q, want it to say the recipe needs amd64", template)
	}
	if got := formatPackageLine(PackageInfo{Name: "hello/1.0"}, false); strings.Contains(got, "needs") {
		t.Errorf("a buildable recipe was marked: %q", got)
	}
}

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
