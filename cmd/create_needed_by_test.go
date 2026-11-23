package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckNeededByFlagsRefusesWhatItCannotTake(t *testing.T) {
	if err := checkNeededByFlags(nil); err == nil {
		t.Error("no argument was accepted")
	}
	if err := checkNeededByFlags([]string{"run.sh"}); err != nil {
		t.Errorf("a script was refused: %v", err)
	}
	for flag, set := range map[string]func(){
		"--name":   func() { createName = "x" },
		"--prefix": func() { createPrefix = "x" },
		"--file":   func() { createFile = "x" },
		"--from":   func() { createFrom = "x" },
		"--desc":   func() { createDesc = "x" },
		"--store":  func() { createStore = true },
		"--update": func() { createUpdate = true },
	} {
		set()
		err := checkNeededByFlags([]string{"run.sh"})
		createName, createPrefix, createFile, createFrom, createDesc, createStore, createUpdate = "", "", "", "", "", false, false
		if err == nil || !strings.Contains(err.Error(), flag) {
			t.Errorf("%s: err = %v, want it named", flag, err)
		}
	}
}

func TestNeededScriptsReadsAScriptByItsFirstLine(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"align":         "#!/bin/bash\n#DEP: bwa/0.7.19\n",
		"stats.py":      "#!/usr/bin/env python3\n#DEP: samtools/1.22\n",
		"notes.txt":     "just notes\n",
		"README.md":     "# readme\n",
		"build.sh":      "#!/bin/bash\ncp x $CNT_BUILD_PREFIX\n",
		"sub/inner.sh":  "#!/bin/bash\n",
		"copy/same.sh":  "#!/bin/bash\n",
		"align.sh.orig": "no interpreter\n",
	})

	got, err := neededScripts([]string{dir, filepath.Join(dir, "align")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "align"), filepath.Join(dir, "stats.py")}
	if !slices.Equal(got, want) {
		t.Errorf("scripts = %v, want %v: both interpreter lines, no notes, no recipe, no subdirectory, each once", got, want)
	}
}

func TestNeededScriptsRefusesANamedFileThatIsNotARunnableScript(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"notes.txt": "just notes\n",
		"build.sh":  "#!/bin/bash\ncp x ${CNT_BUILD_PREFIX}/bin\n",
		"job.sub":   "#!/bin/bash\n",
	})
	for name, want := range map[string]string{
		"notes.txt": "first line must be",
		"build.sh":  "build recipe",
		"job.sub":   "submit file",
		"missing":   "not found",
	} {
		_, err := neededScripts([]string{filepath.Join(dir, name)})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to say %q", name, err, want)
		}
	}
}

func TestFindSiblingSourceFileTakesAnExplicitTextSpecOnly(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(dir, "env.sqf")

	writeFiles(t, dir, map[string]string{"env.txt": "samtools=1.22\n"})
	if got := findSiblingSourceFile(overlay); got != "" {
		t.Errorf("a plain list was taken as a source: %q", got)
	}
	writeFiles(t, dir, map[string]string{"env.txt": "# made by export\n@EXPLICIT\nhttps://example.org/a.conda\n"})
	if got := findSiblingSourceFile(overlay); got != filepath.Join(dir, "env.txt") {
		t.Errorf("an explicit spec was not taken: %q", got)
	}
	writeFiles(t, dir, map[string]string{"env.sh": "#!/bin/bash\n", "env.yml": "name: x\n"})
	if got := findSiblingSourceFile(overlay); got != filepath.Join(dir, "env.yml") {
		t.Errorf("source = %q, want the .yml, which comes before .sh and .txt", got)
	}
}

func TestNeededFileSourceBuildsOnlyASqf(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"ref.yml": "name: x\n", "other.yml": "name: y\n", "list.txt": "bwa\n"})

	if source, reason := neededFileSource(filepath.Join(dir, "ref.sqf")); source != filepath.Join(dir, "ref.yml") || reason != "" {
		t.Errorf("a .sqf with a source: %q, %q", source, reason)
	}
	for _, tc := range []struct{ overlay, want string }{
		{"other.img", "overlay create"},
		{"other.ext3", "overlay create"},
		{"other.sif", "Apptainer"},
		{"other.sqsh", "other.sqf"},
		{"alone.sqf", "no source file"},
		{"list.sqf", "list.txt has no @EXPLICIT"},
	} {
		source, reason := neededFileSource(filepath.Join(dir, tc.overlay))
		if source != "" || !strings.Contains(reason, tc.want) {
			t.Errorf("%s: %q, %q, want a refusal saying %q", tc.overlay, source, reason, tc.want)
		}
	}
}

// A script needs its #DEP: and every overlay a slot lists, each once, and a relative file is read against the current directory.
func TestNeededOverlaysListsDepsAndEverySlotChoice(t *testing.T) {
	images := installSlotImages(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, images, map[string]string{"tool--1.0.sqf": "", "grch38--genome--gencode.sqf": ""})
	writeFiles(t, cwd, map[string]string{"here.sqf": ""})
	a := filepath.Join(cwd, "a.sh")
	b := filepath.Join(cwd, "b.sh")
	writeFiles(t, cwd, map[string]string{
		"a.sh": "#!/bin/bash\n#DEP: tool/1.0\n#DEP: here.sqf\n#DEP: gone.sqf\n" +
			"#SLOT: genome default=grch38/genome/gencode choices=grch38/genome/gencode,grcm39/genome/gencode\n",
		"b.sh": "#!/bin/bash\n#DEP: tool/1.0\n#DEP: absent/2.0\n",
	})

	got, err := neededOverlays(context.Background(), []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]depState{}
	for _, d := range got {
		if _, dup := state[d.Request]; dup {
			t.Errorf("%s listed twice", d.Request)
		}
		state[d.Request] = d.State
	}
	want := map[string]depState{
		"tool/1.0":                     depInstalled,
		filepath.Join(cwd, "here.sqf"): depInstalled,
		filepath.Join(cwd, "gone.sqf"): depMissingFile,
		"absent/2.0":                   depMissingName,
		"grch38/genome/gencode":        depInstalled,
		"grcm39/genome/gencode":        depMissingName,
	}
	for request, st := range want {
		if got, ok := state[request]; !ok || got != st {
			t.Errorf("%s: state %v (listed %v), want %v", request, got, ok, st)
		}
	}
	if len(state) != len(want) {
		t.Errorf("listed %d overlays, want %d: %v", len(state), len(want), state)
	}
}

func TestClassifyNeededSortsByWhatIsDoneAboutEach(t *testing.T) {
	installSlotImages(t)
	settingstest.OverrideList(t, "channels")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"ref.yml": "name: x\n"})

	plan := classifyNeeded(context.Background(), []scriptDep{
		{Request: "tool/1.0", Path: "/images/tool--1.0.sqf"},
		{Request: filepath.Join(dir, "ref.sqf"), State: depMissingFile},
		{Request: filepath.Join(dir, "x.img"), State: depMissingFile},
		{Request: "absent/2.0", State: depMissingName},
	})
	if len(plan.Installed) != 1 || len(plan.Files) != 1 || plan.Files[0].Source != filepath.Join(dir, "ref.yml") {
		t.Errorf("installed %v, files %v", plan.Installed, plan.Files)
	}
	if !slices.Equal(plan.Names, []string{"absent/2.0"}) {
		t.Errorf("names = %v, want the one missing name", plan.Names)
	}
	if len(plan.Refused) != 1 || !strings.Contains(plan.Refused[0].Reason, "overlay create") {
		t.Errorf("refused = %v, want the writable overlay", plan.Refused)
	}
}

// A project pins exact identities, so a script it covers is installed by lock and restore, not by name.
func TestRefuseProjectScriptsNamesTheProjectCommands(t *testing.T) {
	root := newProject(t)
	if err := lock.Publish(root, lock.New()); err != nil {
		t.Fatal(err)
	}
	writeScript(t, root, "run.sh", "#DEP: tool/1.0\n")
	script := filepath.Join(root, "run.sh")

	err := refuseProjectScripts([]string{script})
	if err == nil || !strings.Contains(err.Error(), "project lock") || !strings.Contains(err.Error(), "project restore") {
		t.Errorf("err = %v, want the project commands", err)
	}

	noProjectRequested = true
	t.Cleanup(func() { noProjectRequested = false })
	if err := refuseProjectScripts([]string{script}); err != nil {
		t.Errorf("with --no-project: %v", err)
	}
}

func TestScriptNeedsOfListsADeclarationInOrderWithItsSlot(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.sh": "#!/bin/bash\n#DEP: tool/1.0\n#DEP: here.sqf\n" +
			"#SLOT: genome default=grch38/genome/gencode choices=grch38/genome/gencode,grcm39/genome/gencode\n",
		"empty.sh": "#!/bin/bash\necho hi\n",
	})
	got, err := scriptNeedsOf([]string{filepath.Join(dir, "a.sh"), filepath.Join(dir, "empty.sh")})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, n := range got[0].Needs {
		seen = append(seen, n.Request+"|"+n.Slot)
	}
	want := []string{
		"tool/1.0|",
		mustAbs(t, "here.sqf") + "|",
		"grch38/genome/gencode|slot genome, default",
		"grcm39/genome/gencode|slot genome",
	}
	if !slices.Equal(seen, want) {
		t.Errorf("needs = %v, want %v", seen, want)
	}
	if len(got[1].Needs) != 0 {
		t.Errorf("a script with no declaration lists %v", got[1].Needs)
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestWriteNeededShowsEachScriptThenHowEachOverlayStands(t *testing.T) {
	plan := neededPlan{
		Installed: []scriptDep{{Request: "bwa/0.7.19"}},
		Files:     []neededFile{{Overlay: "/data/ref.sqf", Source: "/data/ref.yml"}},
		Resolved:  map[string]string{"samtools/1.22": "samtools/1.22.1", "grcm39/genome/gencode": "grcm39/genome/gencode"},
		Refused:   []neededRefusal{{Request: "odd/1", Reason: "is a template"}},
	}
	needs := []scriptNeeds{
		{Script: "/nowhere/a.sh", Needs: []scriptNeed{
			{Request: "samtools/1.22"},
			{Request: "/data/ref.sqf"},
			{Request: "grcm39/genome/gencode", Slot: "slot genome"},
			{Request: "odd/1"},
		}},
		{Script: "/nowhere/b.sh", Needs: []scriptNeed{{Request: "bwa/0.7.19"}}},
		{Script: "/nowhere/c.sh"},
	}
	var out strings.Builder
	writeNeeded(&out, needs, plan)
	got := out.String()
	for _, want := range []string{
		"/nowhere/a.sh\n",
		"✗ samtools/1.22           install as samtools/1.22.1\n",
		"✗ /data/ref.sqf           build from ref.yml\n",
		"✗ grcm39/genome/gencode   install (slot genome)\n",
		"✗ odd/1                   is a template\n",
		"/nowhere/b.sh\n  ✓ bwa/0.7.19\n",
		"/nowhere/c.sh\n  nothing to install\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

func TestNeededStepsListFilesFirstAndTakeTheSourceAsANote(t *testing.T) {
	plan := neededPlan{
		Files: []neededFile{{Overlay: "/data/ref.sqf", Source: "/data/ref.yml"}},
		Names: []string{"bwa/0.7.19"},
	}
	planned := []build.PlannedStep{{Name: "bwa/0.7.19", Path: "/images/bwa--0.7.19.sqf"}}
	steps, pc := neededSteps(plan, planned)
	if len(steps) != 2 || steps[0].Path != "/data/ref.sqf" || steps[1].Name != "bwa/0.7.19" {
		t.Fatalf("steps = %+v", steps)
	}
	if pc.from["/data/ref.sqf"] != "ref.yml" {
		t.Errorf("from = %v", pc.from)
	}
	if pc.typed["bwa/0.7.19"] != "bwa/0.7.19" {
		t.Errorf("a name a script asks for is not typed: %v", pc.typed)
	}
}
