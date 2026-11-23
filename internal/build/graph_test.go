package build

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
)

// The job re-runs create on a node, so every choice the user made on the command
// line that changes what is built has to be in that command.
func TestSchedulerCreateCommandCarriesTheBuildFlags(t *testing.T) {
	got := buildSchedulerCreateCommand([]string{"star/2.7.11b"}, nil, true, true, true, nil)
	for _, want := range []string{"--update", "--store", "--no-prebuilt", "star/2.7.11b"} {
		if !strings.Contains(got, want) {
			t.Errorf("command %q lacks %q", got, want)
		}
	}
	if plain := buildSchedulerCreateCommand([]string{"star/2.7.11b"}, nil, false, false, false, nil); plain != utils.SelfCommand()+" create star/2.7.11b" {
		t.Errorf("a plain build renders %q", plain)
	}
}

// A flag value is quoted, so a channel or source name cannot break the job script.
func TestSchedulerCreateCommandQuotesFlagValues(t *testing.T) {
	got := buildSchedulerCreateCommand([]string{"star/2.7.11b"}, []string{"--channel", "my chan; rm -rf /"}, false, false, false, nil)
	want := utils.SelfCommand() + " create --channel 'my chan; rm -rf /' star/2.7.11b"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// A store build is filed beside the installed name, so an installed name is no
// reason for the graph to skip it — that is what `create --store` is for.
func TestGraphDoesNotSkipAnInstalledNameForAStoreBuild(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo--1.sqf")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	plain := &BuildObject{spec: Spec{Image: ImageSpec{Name: "demo/1"}}, tgt: targetFor(target)}
	store := &BuildObject{spec: Spec{Image: ImageSpec{Name: "demo/1"}}, tgt: targetFor(target), storeOverflow: true}
	bg := &BuildGraph{}

	if !bg.installedAlready(plain) {
		t.Error("an installed name is not skipped")
	}
	if bg.installedAlready(store) {
		t.Error("an installed name is skipped for a store build")
	}
}

// A planned step lists the dependencies its key was derived from, each with its
// role, and an installed one with where it is.
func TestPlannedStepListsTheDependenciesOfItsKey(t *testing.T) {
	equiv := meta.KeyRef{Scheme: "script-equiv-v1", SHA256: "e90d"}
	b := planNode("grch38/genome/gencode", artifact.TypeData, prebuiltNone)
	b.plannedEdges = []meta.Dependency{
		{Name: "samtools/1.24", Type: artifact.TypeApp, Role: meta.RoleHistory},
		{Name: "grch38/transcript/gencode/47", Type: artifact.TypeData, Role: meta.RoleData, Equiv: equiv},
	}
	b.plannedDepPaths = map[string]string{"samtools/1.24": "/images/samtools--1.24.sqf"}

	deps := b.PlannedStep(t.Context()).Deps
	if len(deps) != 2 {
		t.Fatalf("deps = %+v", deps)
	}
	if d := deps[0]; d.Role != meta.RoleHistory || !d.Installed || d.Path != "/images/samtools--1.24.sqf" {
		t.Errorf("installed history dep = %+v", d)
	}
	if d := deps[1]; d.Role != meta.RoleData || d.Installed || d.Path != "" || d.Equiv != equiv {
		t.Errorf("planned data dep = %+v", d)
	}
}

// A node whose dependency is a job is a job too: a foreground build runs before
// any job is submitted, so it would find the dependency missing.
func TestSplitWorkQueuesADependentBehindAJobDependency(t *testing.T) {
	settingstest.OverrideList(t, "build.always_submit", "app")

	node := func(name string, typ artifact.Type, deps ...string) *BuildObject {
		b := planNode(name, typ, prebuiltNone, deps...)
		b.submitJob = true
		return b
	}
	tool := node("tool/1", artifact.TypeApp)
	data := node("data/1", artifact.TypeData, "tool/1")
	other := node("other/1", artifact.TypeData)
	bg := &BuildGraph{graph: map[string]*BuildObject{"tool/1": tool, "data/1": data, "other/1": other}}

	bg.splitWork([]*BuildObject{tool, data, other}, true)
	if !slices.Equal(objectNames(bg.schedulerBuilds), []string{"tool/1", "data/1"}) {
		t.Errorf("jobs = %v, want the app and its data dependent", objectNames(bg.schedulerBuilds))
	}
	if !slices.Equal(objectNames(bg.localBuilds), []string{"other/1"}) {
		t.Errorf("foreground = %v, want only the build with no job dependency", objectNames(bg.localBuilds))
	}
}

// A job whose dependency runs in the foreground needs no change: the foreground build is finished first.
func TestSplitWorkKeepsAForegroundDependencyInTheForeground(t *testing.T) {
	settingstest.OverrideList(t, "build.always_submit", "data")

	tool := planNode("tool/1", artifact.TypeApp, prebuiltNone)
	tool.submitJob = true
	data := planNode("data/1", artifact.TypeData, prebuiltNone, "tool/1")
	data.submitJob = true
	bg := &BuildGraph{graph: map[string]*BuildObject{"tool/1": tool, "data/1": data}}

	bg.splitWork([]*BuildObject{tool, data}, true)
	if !slices.Equal(objectNames(bg.localBuilds), []string{"tool/1"}) || !slices.Equal(objectNames(bg.schedulerBuilds), []string{"data/1"}) {
		t.Errorf("foreground = %v, jobs = %v", objectNames(bg.localBuilds), objectNames(bg.schedulerBuilds))
	}
}

// Nothing is promoted when jobs cannot be submitted, for a build a job cannot reproduce, or for a pull.
func TestSplitWorkPromotesOnlyWhatAJobCanBuild(t *testing.T) {
	settingstest.OverrideList(t, "build.always_submit", "app")

	tool := planNode("tool/1", artifact.TypeApp, prebuiltNone)
	tool.submitJob = true
	unreproducible := planNode("conda/1", artifact.TypeData, prebuiltNone, "tool/1") // submitJob is false
	pulled := planNode("pulled/1", artifact.TypeData, prebuiltPull, "tool/1")
	pulled.submitJob = true
	graph := map[string]*BuildObject{"tool/1": tool, "conda/1": unreproducible, "pulled/1": pulled}

	bg := &BuildGraph{graph: graph}
	bg.splitWork([]*BuildObject{tool, unreproducible, pulled}, true)
	if !slices.Equal(objectNames(bg.schedulerBuilds), []string{"tool/1"}) {
		t.Errorf("jobs = %v, want only the app", objectNames(bg.schedulerBuilds))
	}

	bg = &BuildGraph{graph: graph}
	bg.splitWork([]*BuildObject{tool, unreproducible, pulled}, false)
	if len(bg.schedulerBuilds) != 0 {
		t.Errorf("jobs = %v with submission off", objectNames(bg.schedulerBuilds))
	}
}

func objectNames(objs []*BuildObject) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.NameVersion()
	}
	return out
}
