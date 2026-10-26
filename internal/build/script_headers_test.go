package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetTypeFromScript(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantType string
		wantErr  bool
	}{
		{
			name:     "NoTypeDefaultsToApp",
			content:  "#!/bin/bash\necho hello\n",
			wantType: "app",
		},
		{
			name:     "HashTypeData",
			content:  "#!/bin/bash\n#TYPE:data\n",
			wantType: "data",
		},
		{
			// A bare TYPE: has no '#', so it is not an annotation at all and the
			// script simply declares nothing.
			name:     "BareTypeIsNotAnAnnotation",
			content:  "#!/bin/bash\nTYPE:data\n",
			wantType: "app",
		},
		{
			name:    "InvalidType",
			content: "#!/bin/bash\n#TYPE:unknown\n",
			wantErr: true,
		},
		// Aliases are gone: #TYPE: has to mean the same thing here as it does to
		// catalog.DeriveType, which only ever accepted app and data.
		{
			name:    "RejectsFormerRefAlias",
			content: "#!/bin/bash\n#TYPE:ref\n",
			wantErr: true,
		},
		{
			name:    "RejectsFormerToolAlias",
			content: "#!/bin/bash\n#TYPE:tool\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp, err := os.CreateTemp("", "script-type-*.sh")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer os.Remove(tmp.Name())

			if _, err := tmp.WriteString(tt.content); err != nil {
				t.Fatalf("failed to write temp file: %v", err)
			}
			tmp.Close()

			got, err := GetTypeFromScript(tmp.Name())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantType {
				t.Fatalf("GetTypeFromScript() = %q, want %q", got, tt.wantType)
			}
		})
	}
}

func TestGetTargetFromScript(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "NoTargetIsEmpty",
			content: "#!/bin/bash\necho hello\n",
			want:    "",
		},
		{
			name:    "DeclaredTarget",
			content: "#!/bin/bash\n#TARGET: star/2.7.11b/index\n",
			want:    "star/2.7.11b/index",
		},
		{
			// The same normalization every other name goes through, so a script
			// and a recipe cannot disagree about what they named.
			name:    "TargetIsNormalized",
			content: "#!/bin/bash\n#TARGET: /star/2.7.11b/index/\n",
			want:    "star/2.7.11b/index",
		},
		{
			// A bare TARGET: has no '#', so it is not an annotation at all.
			name:    "BareTargetIsNotAnAnnotation",
			content: "#!/bin/bash\nTARGET: star/2.7.11b\n",
			want:    "",
		},
		{
			// #TARGET: doubles as a template pattern for catalog recipes, where
			// #PH: supplies the values. An external build is addressed by path
			// and has neither, so a pattern could never be filled in.
			name:    "RejectsAPlaceholder",
			content: "#!/bin/bash\n#TARGET: star/{star_version}/index\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "build.sh")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("failed to write script: %v", err)
			}

			got, err := GetTargetFromScript(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("GetTargetFromScript() = %q, want %q", got, tt.want)
			}
		})
	}
}

// An empty component would make key.Role's component match silently fail,
// downgrading a real dependency to build history.
func TestGetTargetFromScriptRejectsAnEmptyComponent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\n#TARGET: star//index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := GetTargetFromScript(path); err == nil {
		t.Fatalf("an empty component was accepted as %q", got)
	}
}
