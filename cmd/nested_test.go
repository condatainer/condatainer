package cmd

import (
	"context"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/runtime/container"
)

func TestPlanNested(t *testing.T) {
	const (
		auto = nestedRunAuto
		on   = nestedRunTrue
		off  = nestedRunFalse
	)
	cases := []struct {
		name      string
		mode      string
		libexec   bool
		installed []string
		want      nestedPlan
	}{
		{"false does nothing", off, true, []string{"1.5.3"}, nestedPlan{}},
		{"libexec apptainer is bound", auto, true, []string{"1.5.3"}, nestedPlan{BindLibexec: true}},
		{"libexec apptainer wins under true too", on, true, nil, nestedPlan{BindLibexec: true}},
		{"newest installed overlay is mounted", auto, false, []string{"1.4.0", "1.5.3", "1.5.10"}, nestedPlan{Overlay: "1.5.10"}},
		{"auto builds nothing", auto, false, nil, nestedPlan{}},
		{"true builds when none installed", on, false, nil, nestedPlan{Build: true}},
		{"true mounts an installed one instead of building", on, false, []string{"1.5.3"}, nestedPlan{Overlay: "1.5.3"}},
	}
	for _, c := range cases {
		if got := planNested(c.mode, c.libexec, c.installed); got.BindLibexec != c.want.BindLibexec ||
			got.Overlay != c.want.Overlay || got.Build != c.want.Build {
			t.Errorf("%s: planNested = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestNestedRunKeyNormalizesAndRefuses(t *testing.T) {
	k, _ := settings.Lookup("nested_run")
	for in, want := range map[string]string{"auto": "auto", " TRUE ": "true", "False": "false"} {
		if got, err := k.Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := k.Parse("maybe"); err == nil {
		t.Error("nested_run accepted an unknown value")
	}
}

// With nothing in libexec, nestedRun mounts the newest installed apptainer
// overlay and leaves the caller's list alone; "false" adds nothing.
func TestNestedRunMountsTheInstalledOverlay(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "apptainer--1.5.3.sqf")
	if err := os.WriteFile(stub, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevPaths := config.GlobalDataPaths
	config.GlobalDataPaths.ImagesDirs = []string{dir}
	t.Setenv("CNT_LIBEXEC", filepath.Join(dir, "libexec"))
	build.InvalidateInstalledOverlays()
	container.InvalidateInstalledOverlaysCache()
	t.Cleanup(func() {
		config.GlobalDataPaths = prevPaths
		build.InvalidateInstalledOverlays()
		container.InvalidateInstalledOverlaysCache()
	})

	given := []string{"/x/samtools--1.0.sqf"}
	settingstest.Override(t, "nested_run", nestedRunAuto)
	got, bind, err := nestedRun(context.Background(), given)
	if err != nil || bind {
		t.Fatalf("nestedRun = %v, bind=%v, err=%v", got, bind, err)
	}
	if len(got) != 2 || got[0] != stub || got[1] != given[0] {
		t.Errorf("overlays = %v, want %s first, then the caller's", got, stub)
	}
	if len(given) != 1 {
		t.Error("the caller's slice was modified")
	}

	// The same file already named is not added twice.
	if again, _, _ := nestedRun(context.Background(), got); len(again) != 2 {
		t.Errorf("an overlay already in the list was added again: %v", again)
	}

	settingstest.Override(t, "nested_run", nestedRunFalse)
	if got, _, _ := nestedRun(context.Background(), given); len(got) != 1 {
		t.Errorf("false added overlays: %v", got)
	}
}
