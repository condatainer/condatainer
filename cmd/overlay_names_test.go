package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestResolveOverlayValuesOutsideAProject(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"samtools--1.21.sqf", "samtools--1.22.1.sqf", "ubuntu24--xfce4.sqf"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prevSources, prevPaths := config.Global.Sources, config.GlobalDataPaths
	config.Global.Sources = []catalog.Spec(nil)
	settingstest.Override(t, "default_distro", "ubuntu24")
	config.GlobalDataPaths.ImagesDirs = []string{dir}
	config.ResetCatalog()
	t.Cleanup(func() {
		config.Global.Sources, config.GlobalDataPaths = prevSources, prevPaths
		config.ResetCatalog()
	})
	t.Chdir(t.TempDir())

	got, err := resolveOverlayValues(context.Background(),
		[]string{"samtools", "samtools/1.22:ro", "xfce4", "nothing/1.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, "samtools--1.22.1.sqf"),
		filepath.Join(dir, "samtools--1.22.1.sqf") + ":ro",
		filepath.Join(dir, "ubuntu24--xfce4.sqf"),
		"nothing/1.0",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("overlay %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveOverlayValuesRefusesAConstraint(t *testing.T) {
	if _, err := resolveOverlayValues(context.Background(), []string{"star>=2.7.0"}, nil); err == nil {
		t.Error("a version constraint was accepted")
	}
}

// One rule says what a run mounts for each #DEP:.
func TestScriptDepsFollowsTheRuleARunMounts(t *testing.T) {
	dir := installSlotImages(t)
	for _, name := range []string{"tool--1.0.sqf", "mytool.sqf"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("here.sqf", nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		dep   string
		state depState
		path  string
	}{
		{"tool/1.0", depInstalled, filepath.Join(dir, "tool--1.0.sqf")},
		{"tool", depInstalled, filepath.Join(dir, "tool--1.0.sqf")}, // the solver completes a bare name
		{"mytool", depInstalled, filepath.Join(dir, "mytool.sqf")},  // the installed map answers a name with no version
		{"absent/1.0", depMissingName, "absent/1.0"},
		{"here.sqf", depInstalled, "here.sqf"},
		{"gone.sqf", depMissingFile, "gone.sqf"},
	} {
		got, _, err := scriptDeps(context.Background(), []string{tc.dep})
		if err != nil {
			t.Fatalf("%s: %v", tc.dep, err)
		}
		if got[0].State != tc.state || got[0].Path != tc.path {
			t.Errorf("%s: state %v, path %q; want state %v, path %q", tc.dep, got[0].State, got[0].Path, tc.state, tc.path)
		}
	}

	if _, _, err := scriptDeps(context.Background(), []string{"tool>=1.0"}); err == nil {
		t.Error("a version constraint was accepted")
	}
}
