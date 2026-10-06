package config

import (
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/catalog"
)

func TestEnsureDefaultDistroRecordsOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.json"),
		[]byte(`{"schema":1,"default_distro":"ubuntu24"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	settingstest.Override(t, "default_distro", "")
	Global.Sources = []catalog.Spec{{Name: "cnt", Base: root}}
	ResetCatalog()
	t.Cleanup(func() { recommendedDistro = ""; Global.Sources = nil; ResetCatalog() })

	cat, err := OpenCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if got := EnsureDefaultDistro(cat); got != "ubuntu24" {
		t.Fatalf("EnsureDefaultDistro = %q, want ubuntu24", got)
	}
	if DefaultDistro() != "ubuntu24" {
		t.Errorf("DefaultDistro = %q", DefaultDistro())
	}
	if BaseRecipeName() != "ubuntu24/base" {
		t.Errorf("BaseRecipeName = %q", BaseRecipeName())
	}

	// Sticky: a later upstream default does not revise what was recorded.
	settingstest.Override(t, "default_distro", "ubuntu22")
	if got := EnsureDefaultDistro(cat); got != "ubuntu22" {
		t.Errorf("EnsureDefaultDistro overwrote a recorded distro: %q", got)
	}
	if def := SourceDefaultDistro(cat); def != "ubuntu24" {
		t.Errorf("SourceDefaultDistro = %q, want the source's newer recommendation", def)
	}
}
