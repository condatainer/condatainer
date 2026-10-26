package build

import (
	"errors"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/catalog"
)

// Only data may declare #DEP:. An app is prebuilt and an OS is self-contained.
func TestValidateRecipeDependencyDeclarations(t *testing.T) {
	tests := []struct {
		path    string
		text    string
		wantErr bool
	}{
		{"grch38/gtf/49", "#DEP:samtools/1.23.1\necho\n", false},
		{"samtools/1.23.1", "#DEP:zlib/1.3\necho\n", true},
		{"samtools/1.23.1", "#TYPE:app\n#DEP:zlib/1.3\necho\n", true},
		{"ubuntu24/r-essential.def", "#DEP:zlib/1.3\nBootstrap: docker\n", true},
		{"ubuntu24/base.def", "#DEP:zlib/1.3\nBootstrap: docker\n", true},
		{"samtools/1.23.1", "echo\n", false},
	}
	for _, tt := range tests {
		rec, err := catalog.ParseRecipe(tt.path, strings.NewReader(tt.text))
		if err != nil {
			t.Fatal(err)
		}
		err = validateRecipe(rec)
		if tt.wantErr && err == nil {
			t.Errorf("%s (%s) accepted a #DEP:", tt.path, rec.Type)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("%s (%s) rejected: %v", tt.path, rec.Type, err)
		}
		if tt.wantErr && !errors.Is(err, catalog.ErrInvalidRecipe) {
			t.Errorf("%s: err = %v, want catalog.ErrInvalidRecipe", tt.path, err)
		}
	}
}

// Only data may depend on anything, and an edge is a name/version rather than a
// path. Both rules live in validateDeps so a catalog recipe and an external
// build cannot answer to different ones.
func TestValidateDeps(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     artifact.Type
		deps    []string
		wantErr []string
	}{
		{name: "DataWithNamedDeps", typ: artifact.TypeData, deps: []string{"samtools/1.21"}},
		{name: "NoDepsIsAlwaysFine", typ: artifact.TypeApp},
		{
			name: "AppMayNotDependOnAnything", typ: artifact.TypeApp, deps: []string{"samtools/1.21"},
			wantErr: []string{"only data has build dependencies"},
		},
		{
			name: "OSMayNotDependOnAnything", typ: artifact.TypeOS, deps: []string{"samtools/1.21"},
			wantErr: []string{"only data has build dependencies"},
		},
		{
			name: "DataMayNotDependOnAPath", typ: artifact.TypeData, deps: []string{"overlays/tool.sqf"},
			wantErr: []string{"not an overlay path"},
		},
		{
			name: "AWritableOverlayIsAPathToo", typ: artifact.TypeData, deps: []string{"env.img"},
			wantErr: []string{"not an overlay path"},
		},
		{
			// Both rules are reported, so one fix does not reveal the other.
			name: "AppWithAPathHearsBoth", typ: artifact.TypeApp, deps: []string{"env.img"},
			wantErr: []string{"only data has build dependencies", "not an overlay path"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDeps("demo/1.0", tc.typ, tc.deps)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, catalog.ErrInvalidRecipe) {
				t.Errorf("error is not catalog.ErrInvalidRecipe: %v", err)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}
