package config

import (
	"errors"
	"strings"
	"testing"

	"path/filepath"
	"reflect"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/spf13/viper"
)

func TestDecodeSourceList(t *testing.T) {
	// What viper hands back for a YAML sequence of single-key mappings.
	raw := []any{
		map[string]any{"cnt": "https://example.invalid/recipes/"},
		map[string]any{"lab": "/shared/lab/recipes"},
	}
	got := decodeSourceList(raw)
	if len(got) != 2 {
		t.Fatalf("got %d specs: %+v", len(got), got)
	}
	// Order comes from the sequence, the handle from the key, and a trailing
	// slash is trimmed so a base never doubles up when a path is joined.
	if got[0].Name != "cnt" || got[0].Base != "https://example.invalid/recipes" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Name != "lab" || got[1].Base != "/shared/lab/recipes" {
		t.Errorf("second = %+v", got[1])
	}

	if got := decodeSourceList("not a list"); got != nil {
		t.Errorf("non-list = %+v, want nil", got)
	}
}

func TestSelectSources(t *testing.T) {
	original := Global.Sources
	t.Cleanup(func() {
		Global.Sources = original
		ResetCatalog()
	})

	configured := []catalog.Spec{
		{Name: "site", Base: "/site"},
		{Name: "lab", Base: "/lab"},
		{Name: "cnt", Base: "https://example.invalid"},
	}

	t.Run("empty keeps configured order", func(t *testing.T) {
		Global.Sources = append([]catalog.Spec(nil), configured...)
		if err := SelectSources(nil); err != nil {
			t.Fatal(err)
		}
		if got := Global.Sources; len(got) != 3 || got[0].Name != "site" || got[2].Name != "cnt" {
			t.Fatalf("sources = %+v", got)
		}
	})

	t.Run("flag order wins and duplicates collapse", func(t *testing.T) {
		Global.Sources = append([]catalog.Spec(nil), configured...)
		if err := SelectSources([]string{"cnt", "site", "cnt"}); err != nil {
			t.Fatal(err)
		}
		if got := Global.Sources; len(got) != 2 || got[0].Name != "cnt" || got[1].Name != "site" {
			t.Fatalf("sources = %+v, want cnt then site", got)
		}
	})

	t.Run("unknown is an error and changes nothing", func(t *testing.T) {
		Global.Sources = append([]catalog.Spec(nil), configured...)
		err := SelectSources([]string{"lab", "missing"})
		if err == nil || !strings.Contains(err.Error(), "unknown source") {
			t.Fatalf("error = %v", err)
		}
		if got := Global.Sources; len(got) != 3 || got[0].Name != "site" {
			t.Fatalf("sources changed after error: %+v", got)
		}
	})
}

// Layers concatenate strongest first, and a name already taken is not repeated:
// a user entry shadows a site entry of the same handle.
func TestResolvedSourcesMergesLayers(t *testing.T) {
	user := viper.New()
	user.SetConfigType("yaml")
	if err := user.ReadConfig(stringReader(`
sources:
  - lab: /user/lab
`)); err != nil {
		t.Fatal(err)
	}
	site := viper.New()
	site.SetConfigType("yaml")
	if err := site.ReadConfig(stringReader(`
sources:
  - lab: /site/lab
  - cnt: https://site.invalid
`)); err != nil {
		t.Fatal(err)
	}

	saved := loadedLayers
	t.Cleanup(func() { loadedLayers = saved })
	loadedLayers = []ConfigLayerInfo{{Type: "user", v: user}, {Type: "app-root", v: site}}

	got := ResolvedSources()
	if len(got) != 2 {
		t.Fatalf("got %+v, want lab and cnt", got)
	}
	if got[0].Name != "lab" || got[0].Base != "/user/lab" || got[0].Layer != "user" {
		t.Errorf("first = %+v, want the user's lab to win", got[0])
	}
	if got[1].Name != "cnt" || got[1].Layer != "app-root" {
		t.Errorf("second = %+v, want the site's cnt to survive", got[1])
	}
}

// The default collection is always reachable, however the list is written, and
// always last so every configured entry outranks it.
func TestLayerSourcesAppendsDefault(t *testing.T) {
	useLayers := func(t *testing.T, yaml string) {
		t.Helper()
		v := viper.New()
		v.SetConfigType("yaml")
		if err := v.ReadConfig(stringReader(yaml)); err != nil {
			t.Fatal(err)
		}
		saved := loadedLayers
		t.Cleanup(func() { loadedLayers = saved })
		loadedLayers = []ConfigLayerInfo{{Type: "user", v: v}}
	}

	// No `sources` key at all: a fresh install resolves recipes unconfigured.
	t.Run("absent", func(t *testing.T) {
		useLayers(t, "base: ubuntu24\n")
		got := layerSources()
		if len(got) != 1 || got[0] != defaultSource {
			t.Fatalf("layerSources = %+v, want just the default", got)
		}
	})

	// An explicit list keeps its own order and gains the default at the end.
	t.Run("appended last", func(t *testing.T) {
		useLayers(t, "sources:\n  - lab: /shared/lab\n")
		got := layerSources()
		if len(got) != 2 || got[0].Name != "lab" || got[1] != defaultSource {
			t.Fatalf("layerSources = %+v, want lab then the default", got)
		}
	})

	// Redefining the handle replaces the default rather than duplicating it:
	// that is how a site points `cnt` at its own collection.
	t.Run("redefined wins", func(t *testing.T) {
		useLayers(t, "sources:\n  - cnt: /site/recipes\n")
		got := layerSources()
		if len(got) != 1 || got[0].Base != "/site/recipes" {
			t.Fatalf("layerSources = %+v, want only the site's cnt", got)
		}
	})

}

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }

func TestPlaceSource(t *testing.T) {
	list := []catalog.Spec{{Name: "a", Base: "/a"}, {Name: "b", Base: "/b"}, {Name: "c", Base: "/c"}}
	names := func(l []catalog.Spec) string {
		var out []string
		for _, s := range l {
			out = append(out, s.Name)
		}
		return strings.Join(out, ",")
	}
	tests := []struct {
		spec catalog.Spec
		pos  Position
		want string
	}{
		{catalog.Spec{Name: "n", Base: "/n"}, Position{}, "a,b,c,n"},
		{catalog.Spec{Name: "n", Base: "/n"}, Position{First: true}, "n,a,b,c"},
		{catalog.Spec{Name: "n", Base: "/n"}, Position{Before: "b"}, "a,n,b,c"},
		{catalog.Spec{Name: "n", Base: "/n"}, Position{After: "b"}, "a,b,n,c"},
		{catalog.Spec{Name: "b", Base: "/new"}, Position{}, "a,b,c"},
		{catalog.Spec{Name: "c", Base: "/c"}, Position{First: true}, "c,a,b"},
		{catalog.Spec{Name: "a", Base: "/a"}, Position{Last: true}, "b,c,a"},
		{catalog.Spec{Name: "a", Base: "/a"}, Position{After: "c"}, "b,c,a"},
	}
	for _, tt := range tests {
		got, err := PlaceSource(list, tt.spec, tt.pos)
		if err != nil || names(got) != tt.want {
			t.Errorf("%s at %+v = %s, %v; want %s", tt.spec.Name, tt.pos, names(got), err, tt.want)
		}
	}
	if got, _ := PlaceSource(list, catalog.Spec{Name: "b", Base: "/new"}, Position{}); got[1].Base != "/new" {
		t.Errorf("replacing kept the old base: %+v", got[1])
	}
	if _, err := PlaceSource(list, catalog.Spec{Name: "n"}, Position{Before: "z"}); !errors.Is(err, ErrSourceNotFound) {
		t.Errorf("before a missing source: %v", err)
	}
}

func TestLayerSourceListRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	want := []catalog.Spec{{Name: "lab", Base: "/shared/lab"}, {Name: "cnt", Base: "https://mirror.invalid"}}
	if err := WriteLayerSourceList(path, want); err != nil {
		t.Fatal(err)
	}
	if got := LayerSourceList(path); !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	if err := WriteLayerSourceList(path, nil); err != nil {
		t.Fatal(err)
	}
	if got := LayerSourceList(path); len(got) != 0 {
		t.Errorf("an empty list left %+v", got)
	}
}
