package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
)

// Each planned step becomes the action it is: an installed overlay is adopted,
// a pull fetches with its copy's keys, and a build says where it runs.
func TestReportStepNamesTheAction(t *testing.T) {
	identity := meta.KeyRef{Scheme: "definition-identity-v1", SHA256: "9f2c"}
	installed := filepath.Join(t.TempDir(), "tool--1.sqf")
	if err := os.WriteFile(installed, []byte("sqf"), 0o644); err != nil {
		t.Fatal(err)
	}

	adopt := reportStep(build.PlannedStep{Name: "tool/1", Installed: true, Path: installed})
	if adopt.Action != report.ActionAdopt || adopt.Size != 3 || adopt.Format != report.FormatSquashFS {
		t.Errorf("adopt = %+v", adopt)
	}

	fetch := reportStep(build.PlannedStep{Name: "os/1", Pull: true, Endpoint: "ghcr.io/lab/cnt",
		Digest: "sha256:abcd", Identity: identity, Path: "/images/os--1.sqf"})
	if fetch.Action != report.ActionFetch || fetch.Endpoint != "ghcr.io/lab/cnt" || fetch.Digest != "sha256:abcd" ||
		fetch.Identity != identity || fetch.Runs != "" || fetch.Size != 0 {
		t.Errorf("fetch = %+v", fetch)
	}

	local := reportStep(build.PlannedStep{Name: "app/1", Path: "/images/app--1.sqf"})
	job := reportStep(build.PlannedStep{Name: "data/1", InJob: true, Path: "/images/data--1.sqf"})
	if local.Action != report.ActionBuild || local.Runs != report.RunsLocal || job.Runs != report.RunsJob {
		t.Errorf("local = %+v, job = %+v", local, job)
	}
}

// A step's dependencies keep their role, and an installed one its location.
func TestReportStepCarriesItsDependencies(t *testing.T) {
	step := reportStep(build.PlannedStep{Name: "grch38/salmon/1.10.2/gencode47-k31", Deps: []build.PlannedDep{
		{Name: "salmon/1.10.2", Type: "app", Role: meta.RoleApp},
		{Name: "grch38/transcript/gencode/47", Type: "data", Role: meta.RoleData, Installed: true, Path: "/images/t.sqf"},
	}})
	if len(step.Deps) != 2 {
		t.Fatalf("deps = %+v", step.Deps)
	}
	if d := step.Deps[0]; d.Name != "salmon/1.10.2" || d.Role != meta.RoleApp || d.Installed || d.Path != "" {
		t.Errorf("app dep = %+v", d)
	}
	if d := step.Deps[1]; d.Role != meta.RoleData || !d.Installed || d.Path != "/images/t.sqf" || d.Format != report.FormatSquashFS {
		t.Errorf("installed data dep = %+v", d)
	}
}

func planOf(t *testing.T, steps []build.PlannedStep, pc planContext) string {
	t.Helper()
	var out strings.Builder
	writePlan(&out, steps, pc, false)
	return out.String()
}

func TestPlanLinesNameWhatIsDoneToEachOverlay(t *testing.T) {
	dir := t.TempDir()
	at := func(name string) string { return filepath.Join(dir, name+".sqf") }
	steps := []build.PlannedStep{
		{Name: "samtools/1.22.1", Path: at("samtools--1.22.1")},
		{Name: "samtools/1.25", Path: at("samtools--1.25")},
		{Name: "bwa/0.7.19", Path: at("bwa--0.7.19"), Pull: true, Endpoint: "ghcr.io/condatainer/apps"},
		{Name: "grch38/genome/gencode", Path: at("grch38--genome--gencode"), InJob: true, DependsOn: []string{"samtools/1.25"}},
		{Name: "cellranger/9.0.1", Path: at("cellranger--9.0.1"), RequiresInput: true},
	}
	typed := map[string]string{
		"samtools/1.22.1":       "samtools/1.22",
		"bwa/0.7.19":            "bwa/0.7.19",
		"grch38/genome/gencode": "grch38/genome/gencode",
		"cellranger/9.0.1":      "cellranger/9.0.1",
	}
	got := planOf(t, steps, planContext{typed: typed, dir: dir})
	for _, want := range []string{
		"Plan: 5 overlays into " + dir + " (",
		"samtools/1.22.1         build locally    typed samtools/1.22\n",
		"samtools/1.25           build locally    for grch38/genome/gencode\n",
		"bwa/0.7.19              pull from ghcr.io\n",
		"grch38/genome/gencode   build by job\n",
		"cellranger/9.0.1        build locally    requires input\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ghcr.io/") {
		t.Errorf("the pull shows more than the registry host:\n%s", got)
	}
}

func TestPlanLinesAnInstalledNameIsALineAboveTheBlock(t *testing.T) {
	dir := t.TempDir()
	steps := []build.PlannedStep{
		{Name: "bwa/0.7.19", Path: filepath.Join(dir, "bwa--0.7.19.sqf"), Installed: true},
		{Name: "samtools/1.22.1", Path: filepath.Join(dir, "samtools--1.22.1.sqf")},
	}
	got := planOf(t, steps, planContext{typed: map[string]string{"bwa/0.7.19": "bwa/0.7.19", "samtools/1.22.1": "samtools/1.22.1"}, dir: dir})
	if !strings.HasPrefix(got, "bwa/0.7.19 is already installed\n\nPlan: 1 overlay into ") {
		t.Errorf("installed line and block:\n%s", got)
	}
	if strings.Count(got, "bwa/0.7.19") != 1 {
		t.Errorf("an installed overlay has a row:\n%s", got)
	}

	allInstalled := planOf(t, steps[:1], planContext{dir: dir})
	if allInstalled != "bwa/0.7.19 is already installed\n" {
		t.Errorf("nothing to build still prints a block:\n%s", allInstalled)
	}
}

func TestPlanLinesShowAFileOutsideTheImagesDirByItsPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	here := filepath.Join(wd, "ref.sqf")
	under := filepath.Join(wd, "out", "refenv.sqf")
	steps := []build.PlannedStep{
		{Name: "ref", Path: here},
		{Name: "mine/1", Path: under},
	}
	got := planOf(t, steps, planContext{})
	for _, want := range []string{
		"Plan: 2 overlays\n",
		"  ./ref.sqf",
		"  out/refenv.sqf   build locally    named mine/1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, " into ") {
		t.Errorf("a plan of paths names an images directory:\n%s", got)
	}
}

func TestPlanLinesRebuildAnInstalledFileUnderUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bwa--0.7.19.sqf")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	steps := []build.PlannedStep{{Name: "bwa/0.7.19", Path: path, InJob: true}}
	got := planOf(t, steps, planContext{dir: dir, update: true})
	if !strings.Contains(got, "rebuild by job") {
		t.Errorf("update plan:\n%s", got)
	}
}

func TestForNoteNamesTheFirstDependent(t *testing.T) {
	for _, tc := range []struct {
		dependents []string
		want       string
	}{
		{[]string{"a"}, "for a"},
		{[]string{"a", "b"}, "for a +1 more"},
		{[]string{"a", "b", "c", "d"}, "for a +3 more"},
	} {
		if got := forNote(tc.dependents); got != tc.want {
			t.Errorf("forNote(%v) = %q, want %q", tc.dependents, got, tc.want)
		}
	}
}

func TestPlanLinesAnOverlayTheUserTypedHasNoForNote(t *testing.T) {
	dir := t.TempDir()
	steps := []build.PlannedStep{
		{Name: "samtools/1.25", Path: filepath.Join(dir, "a.sqf")},
		{Name: "grch38/genome/gencode", Path: filepath.Join(dir, "b.sqf"), DependsOn: []string{"samtools/1.25"}},
	}
	typed := map[string]string{"samtools/1.25": "samtools/1.25", "grch38/genome/gencode": "grch38/genome/gencode"}
	if got := planOf(t, steps, planContext{typed: typed, dir: dir}); strings.Contains(got, " for ") {
		t.Errorf("a typed name carries a for note:\n%s", got)
	}
}

func TestPrintPlanIsHiddenByQuiet(t *testing.T) {
	was := utils.QuietMode
	utils.QuietMode = true
	defer func() { utils.QuietMode = was }()
	var out strings.Builder
	printPlan(&out, []build.PlannedStep{{Name: "bwa/0.7.19", Path: "/x/bwa.sqf"}}, true)
	if out.Len() != 0 {
		t.Errorf("-q printed %q", out.String())
	}
}

func TestWritePlanEndsADryRunWithItsLine(t *testing.T) {
	var out strings.Builder
	writePlan(&out, nil, planContext{}, true)
	if out.String() != "Dry run: nothing was built, pulled or submitted.\n" {
		t.Errorf("dry run of nothing: %q", out.String())
	}
}

func TestPlanPathKeepsAFileInTheWorkingDirectoryAPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(wd, "x.sqf"):               "./x.sqf",
		filepath.Join(wd, "out", "x.sqf"):        "out/x.sqf",
		filepath.Join(filepath.Dir(wd), "y.sqf"): filepath.Join(filepath.Dir(wd), "y.sqf"),
	} {
		if got := planPath(path); got != want {
			t.Errorf("planPath(%s) = %q, want %q", path, got, want)
		}
	}
}
