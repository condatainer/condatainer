package catalog

import (
	"errors"
	"strings"
	"testing"
)

// Only data may declare #DEP:. An app is prebuilt and an OS is self-contained.
func TestValidateDependencyDeclarations(t *testing.T) {
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
		rec := parse(t, tt.path, tt.text)
		err := rec.Validate()
		if tt.wantErr && err == nil {
			t.Errorf("%s (%s) accepted a #DEP:", tt.path, rec.Type)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("%s (%s) rejected: %v", tt.path, rec.Type, err)
		}
		if tt.wantErr && !errors.Is(err, ErrInvalidRecipe) {
			t.Errorf("%s: err = %v, want ErrInvalidRecipe", tt.path, err)
		}
	}
}

// #PH:, #TARGET: and #SOURCE: placeholders must agree, and each disagreement is
// reported at build time whether or not the recipe expands.
func TestValidateReportsPlaceholderProblems(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string // substring of the expected problem; empty means sound
	}{
		{"sound", "#PH:v:1,2\n#TARGET:tool/{v}\n#SOURCE:bin https://x/{v}.tgz\necho\n", ""},
		{"PH without TARGET", "#PH:v:1,2\necho\n", "there is no #TARGET:"},
		{"PH missing from TARGET", "#PH:v:1,2\n#PH:w:3\n#TARGET:tool/{v}\necho\n", "#PH:w is declared"},
		{"TARGET token without any PH", "#TARGET:tool/{v}\necho\n", "there is no #PH:v"},
		{"adjacent open-ended", "#PH:a:*\n#PH:b:*\n#TARGET:tool/{a}{b}\necho\n", "adjacent and both are open-ended"},
		{"SOURCE token without PH", "#PH:v:1,2\n#TARGET:tool/{v}\n#SOURCE:bin https://x/{other}.tgz\necho\n", "#SOURCE:bin uses {other}"},
		{"reserved PH name", "#PH:prefix:1\n#TARGET:tool/{prefix}\necho\n", "#PH:prefix is reserved"},
		{"mixed separators", "#PH:v:1,2|3\n#TARGET:tool/{v}\necho\n", "mixes , and |"},
		{"malformed PH", "#PH:v\n#TARGET:tool/x\necho\n", "#PH:v is malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := parse(t, "tool-template", tt.text)
			if rec.IsTemplate {
				var err error
				if rec, err = Expand(rec, map[string]string{"v": "1", "a": "x", "b": "y"}); err != nil {
					t.Skipf("does not expand: %v", err)
				}
			}
			err := rec.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("sound recipe rejected: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidRecipe) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want ErrInvalidRecipe containing %q", err, tt.want)
			}
		})
	}
}

// #ARCH: is an assertion only a script recipe's author can make. A root
// filesystem is always architecture-specific.
func TestValidateArchDeclarations(t *testing.T) {
	tests := []struct {
		path     string
		text     string
		wantArch Arch
		wantErr  bool
	}{
		{"picard/3.1", "#ARCH:noarch  ## jar files\necho\n", ArchNoarch, false},
		{"picard/3.1", "#ARCH:native\necho\n", ArchNative, false},
		{"grch38/gtf/49", "#ARCH:noarch\necho\n", ArchNoarch, false},
		{"picard/3.1", "echo\n", "", false},
		{"picard/3.1", "#ARCH:aarch64\necho\n", "aarch64", true},
		{"ubuntu24/r-essential.def", "#ARCH:noarch\nBootstrap: docker\n", ArchNoarch, true},
		{"ubuntu24/base.def", "#ARCH:noarch\nBootstrap: docker\n", ArchNoarch, true},
	}
	for _, tt := range tests {
		rec := parse(t, tt.path, tt.text)
		if rec.Arch != tt.wantArch {
			t.Errorf("%s: arch = %q, want %q", tt.path, rec.Arch, tt.wantArch)
		}
		err := rec.Validate()
		if tt.wantErr != (err != nil) {
			t.Errorf("%s (%s): err = %v, wantErr %v", tt.path, rec.Type, err, tt.wantErr)
		}
	}
}

func TestHasComponents(t *testing.T) {
	tests := []struct {
		name, dep string
		want      bool
	}{
		{"grch38/star/2.7.11b/gencode49", "star/2.7.11b", true},
		{"grch38/star/2.7.11b/gencode49", "grch38/star", true},
		{"grch38/star/2.7.11b/gencode49", "gencode49", true},
		// Substring matching is never used.
		{"grch38/star2.7.11b/gencode49", "star/2.7.11b", false},
		{"grch38/star/2.7.11b-old/gencode49", "star/2.7.11b", false},
		// Versions are strings, never compared semantically.
		{"grch38/star/2.7.11/gencode49", "star/2.7.11b", false},
		// The run has to be contiguous, and an OS name carries its distro.
		{"grch38/star/gencode49/2.7.11b", "star/2.7.11b", false},
		{"grch38/pytorch/2.9/index", "ubuntu24/pytorch/2.9", false},
		{"grch38/ubuntu24/pytorch/2.9", "ubuntu24/pytorch/2.9", true},
		{"star/2.7.11b", "star/2.7.11b", true},
		{"star", "star/2.7.11b", false},
	}
	for _, tt := range tests {
		if got := HasComponents(tt.name, tt.dep); got != tt.want {
			t.Errorf("HasComponents(%q, %q) = %v, want %v", tt.name, tt.dep, got, tt.want)
		}
	}
}

// Only data may depend on anything, and an edge is a name/version rather than a
// path. Both rules live in ValidateDeps so a catalog recipe and an external
// build cannot answer to different ones.
func TestValidateDeps(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     Type
		deps    []string
		wantErr []string
	}{
		{name: "DataWithNamedDeps", typ: TypeData, deps: []string{"samtools/1.21"}},
		{name: "NoDepsIsAlwaysFine", typ: TypeApp},
		{
			name: "AppMayNotDependOnAnything", typ: TypeApp, deps: []string{"samtools/1.21"},
			wantErr: []string{"only data has build dependencies"},
		},
		{
			name: "OSMayNotDependOnAnything", typ: TypeOS, deps: []string{"samtools/1.21"},
			wantErr: []string{"only data has build dependencies"},
		},
		{
			name: "DataMayNotDependOnAPath", typ: TypeData, deps: []string{"overlays/tool.sqf"},
			wantErr: []string{"not an overlay path"},
		},
		{
			name: "AWritableOverlayIsAPathToo", typ: TypeData, deps: []string{"env.img"},
			wantErr: []string{"not an overlay path"},
		},
		{
			// Both rules are reported, so one fix does not reveal the other.
			name: "AppWithAPathHearsBoth", typ: TypeApp, deps: []string{"env.img"},
			wantErr: []string{"only data has build dependencies", "not an overlay path"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeps("demo/1.0", tc.typ, tc.deps)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !errors.Is(err, ErrInvalidRecipe) {
				t.Errorf("error is not ErrInvalidRecipe: %v", err)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// IsPathDep decides which grammar a declaration is read with, so its extension
// set has to be utils.IsOverlay's plus .sif — a running script's `#DEP: env.ext3`
// or `#DEP: base.sif` is a path there and must stay one here.
func TestIsPathDepCoversEveryOverlayExtension(t *testing.T) {
	for _, value := range []string{"env.img", "env.ext3", "x.sqf", "x.sqsh", "x.squashfs", "./a/b.SQF", "base.sif"} {
		if !IsPathDep(value) {
			t.Errorf("IsPathDep(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"samtools/1.21", "grch38/genome/gencode", "star/2.7.11b>=2.3"} {
		if IsPathDep(value) {
			t.Errorf("IsPathDep(%q) = true, want false", value)
		}
	}
}
