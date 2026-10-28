package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
)

func TestPresentListOverlayTrustsRecordedType(t *testing.T) {
	tests := []struct {
		name     string
		encoded  string
		runtime  meta.Runtime
		recorded bool
		want     listPresentation
	}{
		{
			name: "deep explicitly app", encoded: "team--tool--1.0", recorded: true,
			runtime: meta.Runtime{Name: "team/tool/1.0", Type: artifact.TypeApp},
			want:    listPresentation{name: "team/tool", version: "1.0"},
		},
		{
			name: "shallow explicitly data", encoded: "reference--1", recorded: true,
			runtime: meta.Runtime{Name: "reference/1", Type: artifact.TypeData},
			want:    listPresentation{name: "reference/1", data: true},
		},
		{
			name: "os", encoded: "ubuntu24--tool--1", recorded: true,
			runtime: meta.Runtime{Name: "ubuntu24/tool/1", Type: artifact.TypeOS},
			want:    listPresentation{name: "ubuntu24/tool/1", version: "(system app)"},
		},
		{
			name: "no metadata: filename depth", encoded: "reference--genome--1", recorded: false,
			want: listPresentation{name: "reference/genome/1", data: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := presentListOverlay(tc.encoded, tc.runtime, tc.recorded); got != tc.want {
				t.Fatalf("presentListOverlay() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// `list` is the by-name view of what is installed. Store entries are addressed
// by identity and reached through the `store` commands, so one name never
// appears here at several identities.
func TestScanOverlaysByDirDoesNotDescendTheStore(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "samtools--1.23.1.sqf"),
		filepath.Join(root, "store", "samtools--1.23.1@abcdef012345.sqf"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o775); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o664); err != nil {
			t.Fatal(err)
		}
	}

	dirs := scanOverlaysByDir([]string{root}, &SearchQuery{})
	if len(dirs) != 1 {
		t.Fatalf("dirs = %v, want one", dirs)
	}
	for name, path := range dirs[0].Paths {
		if filepath.Dir(path) != root {
			t.Errorf("%s listed from %s, want only the flat root", name, path)
		}
	}
	if len(dirs[0].Paths) > 1 {
		t.Errorf("paths = %v, want only the flat image", dirs[0].Paths)
	}
}

// Each image is one record, nearest directory first. The name, type and
// architecture come from the runtime document, a name already seen nearer is
// shadowed, and a file the image disagrees with, or whose runtime cannot be
// read, is a problem.
func TestListDocHasOneRecordPerImage(t *testing.T) {
	near, far := t.TempDir(), t.TempDir()
	write := func(dir, name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("sqf"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	rt := func(name string, typ artifact.Type) meta.Runtime {
		return meta.Runtime{SchemaVersion: 1, Name: name, Type: typ, Platform: meta.Platform{OS: "linux", Arch: "amd64"}}
	}
	results := []DirOverlays{
		{Dir: near, Files: []ListedFile{
			{Path: write(near, "ubuntu24--base.sqf"), Runtime: rt("ubuntu24/base", artifact.TypeOS)},
			{Path: write(near, "star--2.7.11a.sqf"), Runtime: rt("star/2.7.11b", artifact.TypeApp)},
			{Path: write(near, "broken.sqf"), RuntimeErr: errors.New("no runtime")},
		}},
		{Dir: far, Files: []ListedFile{
			{Path: write(far, "ubuntu24--base.sqf"), Runtime: rt("ubuntu24/base", artifact.TypeOS)},
		}},
	}

	keys := &meta.Keys{Identity: meta.KeyRef{Scheme: "script-identity-v1", SHA256: "9f2c"}}
	doc := listDoc(results, "ubuntu24", func(string) (*meta.Keys, error) { return keys, nil })
	if len(doc.Overlays) != 4 {
		t.Fatalf("overlays = %+v", doc.Overlays)
	}
	base, star, broken, shadowed := doc.Overlays[0], doc.Overlays[1], doc.Overlays[2], doc.Overlays[3]
	if base.Name != "ubuntu24/base" || base.Type != artifact.TypeOS || base.Arch != "amd64" || base.Alias != "base" ||
		base.Format != report.FormatSquashFS || base.Layout != store.LayoutFlat || base.Dir != near || base.Size != 3 ||
		base.Shadowed || base.Runtime == nil || base.Runtime.Name != "ubuntu24/base" {
		t.Errorf("base = %+v", base)
	}
	if base.Keys == nil || base.Keys.Identity.SHA256 != "9f2c" || broken.Keys != nil {
		t.Errorf("keys: base = %+v, broken = %+v", base.Keys, broken.Keys)
	}
	if star.Name != "star/2.7.11b" || star.Alias != "" {
		t.Errorf("star = %+v", star)
	}
	if broken.Name != "broken" || broken.Type != "" || broken.Runtime != nil {
		t.Errorf("broken = %+v", broken)
	}
	if !shadowed.Shadowed || shadowed.Dir != far {
		t.Errorf("far copy = %+v", shadowed)
	}
	if len(doc.Problems) != 2 {
		t.Errorf("problems = %+v, want the misnamed file and the unreadable one", doc.Problems)
	}
}
