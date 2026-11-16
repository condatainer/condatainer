package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/project/lock"
)

// MatchesOrAliasWithText matches against a name and a description: AND terms may be
// split across the two, while anchored patterns still match the name on its own.
func TestMatchesOrAliasWithText(t *testing.T) {
	const (
		name        = "grch38/cellranger/2024-A"
		description = "Cell Ranger GRCh38 2024-A index"
	)

	cases := []struct {
		desc        string
		terms       []string
		name        string
		description string
		want        bool
	}{
		{"term in name only", []string{"cellranger"}, name, description, true},
		{"term in description only", []string{"index"}, name, description, true},
		{"AND split across both fields", []string{"cellranger", "index"}, name, description, true},
		{"AND with one term absent", []string{"cellranger", "bowtie"}, name, description, false},
		{"AND both terms in description", []string{"cell", "ranger"}, name, description, true},
		{"no term matches", []string{"salmon"}, name, description, false},
		{"empty description falls back to name", []string{"index"}, name, "", false},
		{"empty description still matches name", []string{"cellranger"}, name, "", true},
		// "*server" compiles to ^.*server$, which a name+description concatenation
		// would no longer satisfy.
		{"anchored wildcard matches name", []string{"*server"}, "ubuntu24/code-server", "Code Server IDE in the browser", true},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			q := NewSearchQuery(normalizeFilters(tc.terms), nil)
			if got := q.MatchesOrAliasWithText(tc.name, "", tc.description); got != tc.want {
				t.Errorf("MatchesOrAliasWithText(%q, \"\", %q) with terms %v = %v, want %v",
					tc.name, tc.description, tc.terms, got, tc.want)
			}
		})
	}
}

// An empty query matches everything, with or without a description.
func TestMatchesOrAliasWithText_EmptyQuery(t *testing.T) {
	q := NewSearchQuery(nil, nil)
	if !q.MatchesOrAliasWithText("anything", "", "") {
		t.Error("empty query should match every entry")
	}
}

// writeInstalledImages puts files that are not valid images in the images
// directory, so a completion that opens an image fails to see them as OS layers.
func writeInstalledImages(t *testing.T, names ...string) {
	t.Helper()
	dir, err := config.GetWritableImagesDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("not an image"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(path) })
	}
}

// Completion offers the default distro's short name for every image named under
// it, from the directory listing alone.
func TestOverlayChoicesDistroAlias(t *testing.T) {
	t.Chdir(t.TempDir())
	writeInstalledImages(t, "ubuntu24--base.sqf", "ubuntu24--r--4.6.1.sqf", "fastp--1.1.0.sqf")

	got, _ := overlayChoices(false, true, "")
	want := []string{"base", "fastp/1.1.0", "r/4.6.1", "ubuntu24/base", "ubuntu24/r/4.6.1"}
	if !slices.Equal(got, want) {
		t.Errorf("overlayChoices = %v, want %v", got, want)
	}

	got, _ = overlayChoices(false, true, "r/")
	if want := []string{"r/4.6.1"}; !slices.Equal(got, want) {
		t.Errorf("overlayChoices(prefix r/) = %v, want %v", got, want)
	}
}

// A completion without aliases lists the installed names only.
func TestOverlayChoicesWithoutAliases(t *testing.T) {
	t.Chdir(t.TempDir())
	writeInstalledImages(t, "ubuntu24--base.sqf", "fastp--1.1.0.sqf")

	got, _ := overlayChoices(false, false, "")
	want := []string{"fastp/1.1.0", "ubuntu24/base"}
	if !slices.Equal(got, want) {
		t.Errorf("overlayChoices = %v, want %v", got, want)
	}
}

// Standing in a project, completion offers its pinned names, aliased under the
// project's distro, whether or not anything is installed.
func TestOverlayChoicesOffersProjectPins(t *testing.T) {
	root := newProject(t)
	l := lock.New()
	l.Pins["fastp/1.1.0"] = pinOf(vendorArtifact(t, root, "fastp/1.1.0", "echo fastp\n"))
	l.Pins["rocky9/r/4.6.1"] = pinOf(vendorArtifact(t, root, "rocky9/r/4.6.1", "echo r\n"))
	l.Pins[lock.BaseKey] = pinOf(vendorArtifact(t, root, "rocky9/base", "echo base\n"))
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	writeInstalledImages(t, "fastp--1.1.0.sqf", "ubuntu24--base.sqf")

	got, _ := overlayChoices(false, true, "")
	want := []string{"fastp/1.1.0", "r/4.6.1", "rocky9/r/4.6.1", "ubuntu24/base"}
	if !slices.Equal(got, want) {
		t.Errorf("overlayChoices = %v, want %v", got, want)
	}
}

// Pins belong to launching: a push completes what is installed.
func TestOverlayChoicesWithoutAliasesIgnoresProjectPins(t *testing.T) {
	root := newProject(t)
	l := lock.New()
	l.Pins["fastp/1.1.0"] = pinOf(vendorArtifact(t, root, "fastp/1.1.0", "echo fastp\n"))
	if err := lock.Publish(root, l); err != nil {
		t.Fatal(err)
	}
	writeInstalledImages(t, "star--2.7.11b.sqf")

	got, _ := overlayChoices(false, false, "")
	if want := []string{"star/2.7.11b"}; !slices.Equal(got, want) {
		t.Errorf("overlayChoices = %v, want %v", got, want)
	}
}
