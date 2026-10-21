package scheduler

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestExcludeNodesRendersEachSchedulersDirective(t *testing.T) {
	settingstest.OverrideList(t, "scheduler.exclude_nodes", "node-a", "node-b.example")
	for name, tc := range map[string]struct {
		sched Scheduler
		want  string
	}{
		"slurm":    {newTestSlurmScheduler(), "#SBATCH --exclude=node-a,node-b.example\n"},
		"lsf":      {newTestLsfScheduler(), "#BSUB -m \"all ~node-a ~node-b.example\"\n"},
		"htcondor": {newTestHTCondorScheduler(), "requirements = (Machine != \"node-a\") && (Machine != \"node-b.example\")\n"},
	} {
		t.Run(name, func(t *testing.T) {
			path, err := tc.sched.CreateScriptWithSpec(&JobSpec{Name: "j", Command: "true", Specs: &ScriptSpecs{}}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), tc.want) {
				t.Errorf("script lacks %q:\n%s", tc.want, content)
			}
		})
	}
}

func TestExcludeNodesIsRefusedByPBS(t *testing.T) {
	settingstest.OverrideList(t, "scheduler.exclude_nodes", "node-a")
	_, err := newTestPbsScheduler().CreateScriptWithSpec(&JobSpec{Name: "j", Command: "true", Specs: &ScriptSpecs{}}, t.TempDir())
	if !errors.Is(err, ErrExcludeNodesUnsupported) {
		t.Errorf("err = %v, want ErrExcludeNodesUnsupported", err)
	}
	cleared := &ScriptSpecs{Control: RuntimeConfig{ExcludeNodes: []string{}}}
	if _, err := newTestPbsScheduler().CreateScriptWithSpec(&JobSpec{Name: "j", Command: "true", Specs: cleared}, t.TempDir()); err != nil {
		t.Errorf("an empty job list should submit on PBS: %v", err)
	}
}

func TestExcludeNodesAcceptsHostNamesOnly(t *testing.T) {
	for name, ok := range map[string]bool{"node-01": true, "n1.example.org": true, "a b": false, "a;rm": false, "-x": false, `"q"`: false} {
		if got := validateNodeName(name) == nil; got != ok {
			t.Errorf("validateNodeName(%q) accepted = %v, want %v", name, got, ok)
		}
	}
}

// excludeScript creates a script with the given directive lines and returns the script
// the scheduler generates from its parsed specs, with ctrl applied on top.
func excludeScript(t *testing.T, sched Scheduler, directive string, jobList []string) string {
	t.Helper()
	src := t.TempDir() + "/in.sh"
	if err := os.WriteFile(src, []byte("#!/bin/bash\n"+directive+"\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	specs, err := sched.ReadScriptSpecs(src)
	if err != nil {
		t.Fatal(err)
	}
	if jobList != nil {
		specs.Control.ExcludeNodes = jobList
	}
	path, err := sched.CreateScriptWithSpec(&JobSpec{Name: "j", Command: "true", Specs: specs}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestExcludeNodesJobListBeatsScriptBeatsConfig(t *testing.T) {
	settingstest.OverrideList(t, "scheduler.exclude_nodes", "from-config")
	slurm := newTestSlurmScheduler()

	got := excludeScript(t, slurm, "#SBATCH --exclude=bad[1,3],x", nil)
	if !strings.Contains(got, "#SBATCH --exclude=bad[1,3],x\n") || strings.Contains(got, "from-config") {
		t.Errorf("script directive should replace the config list:\n%s", got)
	}
	got = excludeScript(t, slurm, "#SBATCH --exclude=bad[1,3],x", []string{"from-job"})
	if !strings.Contains(got, "#SBATCH --exclude=from-job\n") || strings.Contains(got, "bad[") {
		t.Errorf("job list should replace the script directive:\n%s", got)
	}
	if got = excludeScript(t, slurm, "#SBATCH -c 1", []string{}); strings.Contains(got, "--exclude") {
		t.Errorf("an empty job list should clear the config list:\n%s", got)
	}
	if got = excludeScript(t, slurm, "#SBATCH -c 1", nil); !strings.Contains(got, "#SBATCH --exclude=from-config\n") {
		t.Errorf("the config list applies when nothing else is set:\n%s", got)
	}

	lsf := newTestLsfScheduler()
	if got = excludeScript(t, lsf, "#BSUB -c 1", []string{}); strings.Contains(got, "#BSUB -m") {
		t.Errorf("an empty job list should clear the config list on LSF:\n%s", got)
	}
	if got = excludeScript(t, newTestHTCondorScheduler(), "request_cpus = 1", []string{}); strings.Contains(got, "requirements") {
		t.Errorf("an empty job list should clear the config list on HTCondor:\n%s", got)
	}
	got = excludeScript(t, lsf, "#BSUB -m hostA", nil)
	if !strings.Contains(got, "#BSUB -m hostA\n") || strings.Contains(got, "from-config") {
		t.Errorf("a script's -m should stand over the config list:\n%s", got)
	}
	got = excludeScript(t, lsf, "#BSUB -m hostA", []string{"from-job"})
	if strings.Contains(got, "hostA") || !strings.Contains(got, "#BSUB -m \"all ~from-job\"\n") {
		t.Errorf("job list should replace the script's -m:\n%s", got)
	}
}
