package catalog

import (
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
)

func TestDeriveType(t *testing.T) {
	tests := []struct {
		name, target string
		isDef        bool
		declared     string
		want         artifact.Type
	}{
		{name: "ubuntu24/build-essential", isDef: true, want: artifact.TypeOS},
		{name: "ubuntu24/r", target: "ubuntu24/r/{version}", isDef: true, want: artifact.TypeOS},
		{name: "cellranger/9.0.1", want: artifact.TypeApp},
		{name: "cytoscape", want: artifact.TypeApp},
		{name: "grch38/genome/gencode", want: artifact.TypeData},
		// A template's kind comes from its target: one slash as a filename,
		// three as the module path it builds.
		{name: "grch38/star-gencode", target: "grch38/star/{star_version}/gencode{gencode_version}-{read_length}", want: artifact.TypeData},
		// #TYPE: overrides app/data only.
		{name: "cellranger/9.0.1", declared: "data", want: artifact.TypeData},
		{name: "grch38/genome/gencode", declared: "app", want: artifact.TypeApp},
		// #TYPE: has no effect on a .def: every one is os regardless.
		{name: "ubuntu24/build-essential", isDef: true, declared: "app", want: artifact.TypeOS},
		{name: "cellranger/9.0.1", declared: "nonsense", want: artifact.TypeApp},
	}
	for _, tt := range tests {
		if got := DeriveType(tt.name, tt.target, tt.isDef, tt.declared); got != tt.want {
			t.Errorf("DeriveType(%q, %q, %v, %q) = %q, want %q",
				tt.name, tt.target, tt.isDef, tt.declared, got, tt.want)
		}
	}
}
