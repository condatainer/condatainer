package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
)

func TestNormalizedToolVersion(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "trimmed", raw: "  2.3.0\n", want: "2.3.0", ok: true},
		{name: "empty", raw: " \n", ok: false},
		{name: "multiline", raw: "2.3.0\nextra", ok: false},
		{name: "too long", raw: strings.Repeat("x", maxRecordedToolVersion+1), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizedToolVersion(tt.raw)
			if got != tt.want || ok != tt.ok {
				t.Errorf("normalizedToolVersion() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestBuildObjectManifestAddsCapturedTools(t *testing.T) {
	b := &BuildObject{
		spec: Spec{
			Image:  ImageSpec{Name: "samtools/1.23.1", Type: artifact.TypeApp},
			Source: SourceSpec{Script: &ScriptSource{}},
		},
		buildTools: meta.BuildTools{
			Condatainer: meta.Tool{Version: "1.4.2"},
			Apptainer:   meta.Tool{Name: "apptainer", Version: "1.4.2"},
		},
	}

	if !b.spec.Manifest().Build.Tools.Empty() {
		t.Fatal("Spec manifest contains execution diagnostics")
	}
	if got := b.Manifest().Build.Tools; got != b.buildTools {
		t.Errorf("manifest tools = %+v, want %+v", got, b.buildTools)
	}
}

// A build records the version of the apptainer it ran under, kept on the build, and Unrecorded when no container step ran.
func TestCaptureCommonBuildToolsReadsTheBuildsApptainer(t *testing.T) {
	script := filepath.Join(t.TempDir(), "apptainer")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'apptainer version 1.5.3'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &BuildObject{spec: Spec{Image: ImageSpec{Name: "samtools/1.23.1", Type: artifact.TypeApp}}, apptainerBin: apptainer.Bin{Path: script}}
	b.captureCommonBuildTools(context.Background())
	if got := b.buildTools.Apptainer; got.Name != "apptainer" || got.Version != "1.5.3" {
		t.Errorf("recorded %+v, want apptainer 1.5.3", got)
	}

	none := &BuildObject{spec: b.spec}
	none.captureCommonBuildTools(context.Background())
	if got := none.buildTools.Apptainer; got.Version != meta.Unrecorded {
		t.Errorf("with no container step recorded %+v, want an unrecorded version", got)
	}
}
