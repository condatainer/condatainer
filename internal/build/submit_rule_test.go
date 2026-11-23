package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
)

// build.always_submit sends a build of a listed kind to the scheduler without
// directives. A build a job cannot reproduce, one made from packages or a file,
// never is, because a job re-runs `create <name>`.
func TestAlwaysSubmitSendsTheListedKindsOnly(t *testing.T) {
	named := func(typ artifact.Type) *BuildObject {
		return &BuildObject{submitJob: true, spec: Spec{Image: ImageSpec{Name: "demo/1", Type: typ}}}
	}
	for name, tc := range map[string]struct {
		listed        []string
		os, app, data bool
	}{
		"nothing listed": {nil, false, false, false},
		"data":           {[]string{"data"}, false, false, true},
		"app":            {[]string{"app"}, false, true, false},
		"os":             {[]string{"os"}, true, false, false},
		"app and data":   {[]string{"app", "data"}, false, true, true},
		"all":            {[]string{"all"}, true, true, true},
	} {
		settingstest.OverrideList(t, "build.always_submit", tc.listed...)
		for typ, want := range map[artifact.Type]bool{artifact.TypeOS: tc.os, artifact.TypeApp: tc.app, artifact.TypeData: tc.data} {
			if got := named(typ).RequiresScheduler(); got != want {
				t.Errorf("%s: a named %s build is submitted = %v, want %v", name, typ, got, want)
			}
		}
	}

	settingstest.OverrideList(t, "build.always_submit", "all")
	if (&BuildObject{spec: Spec{Image: ImageSpec{Name: "demo/1", Type: artifact.TypeData}}}).RequiresScheduler() {
		t.Error("a build with no job arguments is submitted under all")
	}
}

func TestValidateSubmitKindTakesABuildKindOrAll(t *testing.T) {
	for _, kind := range []string{"data", "app", "os", "all"} {
		if err := validateSubmitKind(kind); err != nil {
			t.Errorf("%q refused: %v", kind, err)
		}
	}
	for _, kind := range []string{"", "env", "apps", "none"} {
		if validateSubmitKind(kind) == nil {
			t.Errorf("%q accepted", kind)
		}
	}
}

// A job starts where it was submitted from, unless the recipe names a directory.
func TestUseSubmitDirKeepsTheRecipesOwnDirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	specs := &scheduler.ScriptSpecs{}
	useSubmitDir(specs)
	if specs.Control.WorkDir != wd {
		t.Errorf("WorkDir = %q, want the working directory %q", specs.Control.WorkDir, wd)
	}
	named := &scheduler.ScriptSpecs{}
	named.Control.WorkDir = "/data/work"
	useSubmitDir(named)
	if named.Control.WorkDir != "/data/work" {
		t.Errorf("WorkDir = %q, the recipe's own was replaced", named.Control.WorkDir)
	}
}

// An external shell script with directives is submitted once the arguments that
// rebuild it are known, and never a definition: a job could not reproduce one.
func TestExternalScriptIsSubmittableOnlyWithItsJobArgs(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settingstest.Override(t, "scheduler.submit_job", "true")
	settingstest.Override(t, "scheduler.bin", exe)

	dir := t.TempDir()
	src := filepath.Join(dir, "demo.sh")
	if err := os.WriteFile(src, []byte("#!/usr/bin/env bash\n#DESC:demo\n#SBATCH --cpus-per-task=2\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := FromExternalSource(t.Context(), filepath.Join(dir, "demo"), src, false, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if b.RequiresScheduler() {
		t.Error("an external script is submitted before anything says how to rebuild it")
	}

	b.SetJobArgs([]string{"--prefix", filepath.Join(dir, "demo"), "--file", src})
	if !b.RequiresScheduler() {
		t.Error("an external script with directives is not submitted")
	}
	want := utils.SelfCommand() + " create --prefix " + filepath.Join(dir, "demo") + " --file " + src
	if got := buildSchedulerCreateCommand(b.jobTarget(), nil, false, false, false, nil); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}

	def := &BuildObject{buildType: BuildTypeDef}
	def.SetJobArgs([]string{"--file", "x.def"})
	if def.submitJob {
		t.Error("a definition was made submittable")
	}
}
