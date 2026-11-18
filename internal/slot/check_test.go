package slot

import (
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/slot/slottest"
)

// image packs a .sqf with a runtime document and the files under its prefix, as a build does.
func image(t *testing.T, name string, env []meta.EnvVar, files ...string) string {
	t.Helper()
	return slottest.Image(t, t.TempDir(), name, env, files...)
}

func resolved(name string, path string, env, bin []string) Resolved {
	return Resolved{Binding: Binding{Decl: Decl{Name: name, Env: env, Bin: bin}}, Path: path}
}

func TestCheckEnvReadsTheRuntimeDocument(t *testing.T) {
	img := image(t, "grch38/genome/gencode", []meta.EnvVar{{Key: "GENOME_FASTA", Value: "{prefix}/g.fa"}})

	name, err := Check(resolved("genome", img, []string{"GENOME_FASTA"}, nil))
	if err != nil || name != "grch38/genome/gencode" {
		t.Fatalf("name = %q, err = %v, want the overlay's exact name", name, err)
	}
	_, err = Check(resolved("genome", img, []string{"GENOME_FASTA", "GENOME_FAI"}, nil))
	if err == nil || !strings.Contains(err.Error(), `slot "genome": grch38/genome/gencode does not export GENOME_FAI`) {
		t.Fatalf("err = %v, want the missing variable named", err)
	}
}

func TestCheckBinListsPathFolders(t *testing.T) {
	img := image(t, "trim-galore/2.3.0", []meta.EnvVar{{Key: "PATH", Value: "{prefix}/extra:$PATH"}},
		"bin/trim_galore", "extra/helper")

	if _, err := Check(resolved("trimmer", img, nil, []string{"trim_galore", "helper"})); err != nil {
		t.Fatalf("commands in <prefix>/bin and in an #ENV: PATH folder: %v", err)
	}
	_, err := Check(resolved("trimmer", img, nil, []string{"cutadapt"}))
	if err == nil || !strings.Contains(err.Error(), "puts no cutadapt on PATH") {
		t.Fatalf("err = %v, want the missing command named", err)
	}
}

func TestCheckNothingDeclaredReadsNothingButTheName(t *testing.T) {
	img := image(t, "star/2.7.11b", nil)
	if name, err := Check(resolved("index", img, nil, nil)); err != nil || name != "star/2.7.11b" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
}

func TestCollisionsRefusesAVariableTwoSlotsExportDifferently(t *testing.T) {
	a := image(t, "a/genome", []meta.EnvVar{{Key: "REF", Value: "{prefix}/a.fa"}})
	b := image(t, "b/genome", []meta.EnvVar{{Key: "REF", Value: "{prefix}/b.fa"}})
	err := Collisions([]Resolved{resolved("one", a, []string{"REF"}, nil), resolved("two", b, []string{"REF"}, nil)})
	if err == nil || !strings.Contains(err.Error(), `slots "one" and "two" both export REF with different values: a/genome and b/genome`) {
		t.Fatalf("err = %v, want both overlays named", err)
	}
	// A variable only one slot lists is not a collision.
	if err := Collisions([]Resolved{resolved("one", a, []string{"REF"}, nil), resolved("two", b, nil, nil)}); err != nil {
		t.Errorf("one slot listing the variable: %v", err)
	}
}
