package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/spf13/cobra"
)

func TestInferPushSource(t *testing.T) {
	good := &catalog.Source{Name: "lab", Desc: catalog.Descriptor{
		Source: "https://example.invalid/recipes/",
		OCI:    catalog.OCI{Registry: "registry.invalid/lab", Audience: "restricted"},
	}}
	for _, tc := range []struct {
		name    string
		cat     catalog.Catalog
		want    *catalog.Source
		wantErr string
	}{
		{"unique", catalog.Catalog{good}, good, ""},
		{"absent", nil, nil, "matches 0"},
		{"ambiguous", catalog.Catalog{good, &catalog.Source{Name: "other", Desc: good.Desc}}, nil, "matches 2"},
		{"invalid descriptor", catalog.Catalog{&catalog.Source{Name: "bad", Desc: good.Desc, DescriptorErr: errors.New("bad json")}}, nil, "invalid source descriptor"},
		{"stale", catalog.Catalog{&catalog.Source{Name: "old", Desc: good.Desc, Stale: true}}, nil, "unavailable or stale"},
		{"no registry", catalog.Catalog{&catalog.Source{Name: "noregistry", Desc: catalog.Descriptor{Source: good.Desc.Source}}}, nil, "no OCI registry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := inferPushSource("https://example.invalid/recipes", tc.cat)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("source = %p, error = %v", got, err)
			}
		})
	}
}

func TestRegistryPushCompletionStopsAfterArtifact(t *testing.T) {
	got, directive := registryPushCompletion(nil, []string{"hello/1.0"}, "")
	if len(got) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("completion = %v, %v", got, directive)
	}
}

func TestParseAudience(t *testing.T) {
	if _, err := parseAudience("private"); err == nil {
		t.Error("parseAudience accepted an unknown value")
	}
	if got, err := parseAudience("RESTRICTED"); err != nil || got != registry.Restricted {
		t.Errorf("parseAudience = (%q, %v)", got, err)
	}
}

// An installed name/version is addressed by this system and infers where it
// publishes; a path the user pointed at does not, so it must say where it goes.
func TestFindRegistryArtifactSeparatesManagedFromPointedAt(t *testing.T) {
	// GlobalDataPaths caches the resolved search paths, so a test that ran earlier
	// may already have resolved them from the real environment — setting
	// XDG_DATA_HOME here would not be read. Pin the paths instead.
	images := filepath.Join(t.TempDir(), "images")
	if err := os.MkdirAll(images, 0o755); err != nil {
		t.Fatal(err)
	}
	prevPaths := config.GlobalDataPaths
	config.GlobalDataPaths.ImagesDirs = []string{images}
	t.Cleanup(func() { config.GlobalDataPaths = prevPaths })
	installed := filepath.Join(images, "hello--1.0.sqf")
	if err := os.WriteFile(installed, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "hello.sqf")
	if err := os.WriteFile(external, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findRegistryArtifact("hello/1.0")
	if err != nil || got.path != installed || !got.managed {
		t.Fatalf("name/version = %+v, error = %v; want %s managed", got, err, installed)
	}
	if got, err = findRegistryArtifact(external); err != nil || got.path != external || got.managed {
		t.Fatalf("path = %+v, error = %v; want %s unmanaged", got, err, external)
	}
	if _, err = findRegistryArtifact("absent/9.9"); err == nil {
		t.Error("a name matching nothing resolved anyway")
	}
}

// A push record carries what the plan says, and the name tag leaving an older
// build is a move to this build's leading hex.
func TestPushRecord(t *testing.T) {
	identity := meta.KeyRef{Scheme: "script-identity-v1", SHA256: "9f2c1ab4d3e80000"}
	plan := registry.PublishPlan{
		Name: "star/2.7.11b", Type: artifact.TypeApp, Arch: "amd64", Identity: identity,
		Repository: "ghcr.io/lab/cnt", Reference: "ghcr.io/lab/cnt:star--2.7.11b__9f2c1ab4d3e8__amd64",
		Tags: []string{"a", "b"}, Size: 7, Audience: registry.Restricted,
		MovedTag: "star--2.7.11b__amd64", MovedFrom: "0123456789ab",
	}
	got := pushRecord("/images/star--2.7.11b.sqf", plan)
	if got.Name != "star/2.7.11b" || got.Type != artifact.TypeApp || got.Arch != "amd64" || got.Path != "/images/star--2.7.11b.sqf" ||
		got.Identity != identity || got.Audience != "restricted" || got.Existing || got.Outcome != "" || len(got.Tags) != 2 {
		t.Errorf("record = %+v", got)
	}
	if got.Moved == nil || *got.Moved != (report.Move{Tag: "star--2.7.11b__amd64", From: "0123456789ab", To: "9f2c1ab4d3e8"}) {
		t.Errorf("moved = %+v", got.Moved)
	}
	if pushRecord("x", registry.PublishPlan{}).Moved != nil {
		t.Error("a push that moves no tag has a move")
	}
}
