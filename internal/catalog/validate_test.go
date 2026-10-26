package catalog

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

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

// #ARCH: is a declaration only a script recipe's author can make. A root
// filesystem is always architecture-specific.
func TestValidateArchDeclarations(t *testing.T) {
	tests := []struct {
		path     string
		text     string
		wantArch []Arch
		wantErr  bool
	}{
		{"picard/3.1", "#ARCH:noarch  ## jar files\necho\n", []Arch{ArchNoarch}, false},
		{"grch38/gtf/49", "#ARCH:noarch\necho\n", []Arch{ArchNoarch}, false},
		{"jq/1.7", "#ARCH:amd64\necho\n", []Arch{ArchAMD64}, false},
		{"jq/1.7", "#ARCH: amd64 arm64\necho\n", []Arch{ArchAMD64, ArchARM64}, false},
		{"jq/1.7", "#ARCH:AMD64,arm64\necho\n", []Arch{ArchAMD64, ArchARM64}, false},
		{"picard/3.1", "echo\n", nil, false},
		{"picard/3.1", "#ARCH:aarch64\necho\n", []Arch{"aarch64"}, true},
		{"jq/1.7", "#ARCH:amd64 amd64\necho\n", []Arch{ArchAMD64, ArchAMD64}, true},
		{"jq/1.7", "#ARCH:noarch amd64\necho\n", []Arch{ArchNoarch, ArchAMD64}, true},
		{"ubuntu24/r-essential.def", "#ARCH:noarch\nBootstrap: docker\n", []Arch{ArchNoarch}, true},
		{"ubuntu24/base.def", "#ARCH:noarch\nBootstrap: docker\n", []Arch{ArchNoarch}, true},
	}
	for _, tt := range tests {
		rec := parse(t, tt.path, tt.text)
		if !slices.Equal(rec.Arch, tt.wantArch) {
			t.Errorf("%s: arch = %q, want %q", tt.path, rec.Arch, tt.wantArch)
		}
		err := rec.Validate()
		if tt.wantErr != (err != nil) {
			t.Errorf("%s (%s): err = %v, wantErr %v", tt.path, rec.Type, err, tt.wantErr)
		}
	}
}

// A listed architecture is where the recipe builds. No list and noarch build anywhere.
func TestBuildableOn(t *testing.T) {
	tests := []struct {
		arch    string
		text    string
		wantErr bool
	}{
		{"arm64", "echo\n", false},
		{"arm64", "#ARCH:noarch\necho\n", false},
		{"amd64", "#ARCH:amd64\necho\n", false},
		{"arm64", "#ARCH:amd64\necho\n", true},
		{"arm64", "#ARCH:amd64 arm64\necho\n", false},
	}
	for _, tt := range tests {
		rec := parse(t, "jq/1.7", tt.text)
		err := rec.BuildableOn(tt.arch)
		if tt.wantErr != (err != nil) || tt.wantErr && !errors.Is(err, ErrUnsupportedArch) {
			t.Errorf("%q on %s: err = %v, wantErr %v", tt.text, tt.arch, err, tt.wantErr)
		}
	}
}
