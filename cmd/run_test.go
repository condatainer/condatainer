package cmd

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/spf13/pflag"
)

// nopScheduler is an active scheduler that is never called.
type nopScheduler struct{ scheduler.Scheduler }

// setPlace makes the process look like it is in a job, in a container, both or neither.
func setPlace(t *testing.T, job, container bool) {
	t.Helper()
	for _, k := range []string{"SLURM_JOB_ID", "PBS_JOBID", "LSB_JOBID", "_CONDOR_JOB_AD"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	if job {
		t.Setenv("SLURM_JOB_ID", "1")
	}
	old := runInsideContainer
	runInsideContainer = func() bool { return container }
	t.Cleanup(func() { runInsideContainer = old })
}

func setRunSubmit(t *testing.T, v bool) {
	t.Helper()
	old := runSubmit
	runSubmit = v
	t.Cleanup(func() { runSubmit = old })
}

// useScheduler makes submission available: the binary runs and a scheduler is active.
func useScheduler(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settingstest.Override(t, "scheduler.submit_job", "true")
	settingstest.Override(t, "scheduler.bin", exe)
	scheduler.SetActiveScheduler(nopScheduler{})
	t.Cleanup(scheduler.ClearActiveScheduler)
}

func TestRunsInPlace(t *testing.T) {
	for _, tc := range []struct {
		name                string
		job, container, sub bool
		want                bool
	}{
		{"login node", false, false, false, false},
		{"job", true, false, false, true},
		{"job with --submit", true, false, true, false},
		{"container", false, true, false, true},
		{"container with --submit", false, true, true, true},
		{"job in a container with --submit", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, tc.job, tc.container)
			setRunSubmit(t, tc.sub)
			if got := runsInPlace(); got != tc.want {
				t.Errorf("runsInPlace = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckSubmitFlag(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sub, noSub  bool
		container   bool
		noScheduler bool
		submitOff   bool
		want        error
	}{
		{name: "flag off", container: true},
		{name: "ready", sub: true},
		{name: "with --no-submit", sub: true, noSub: true, want: errSubmitWithNoSubmit},
		{name: "in a container", sub: true, container: true, want: errSubmitInContainer},
		{name: "submission off", sub: true, submitOff: true, want: errSubmitNoScheduler},
		{name: "no scheduler", sub: true, noScheduler: true, want: errSubmitNoScheduler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, true, tc.container)
			setRunSubmit(t, tc.sub)
			useScheduler(t)
			if tc.noScheduler {
				scheduler.ClearActiveScheduler()
			}
			if tc.submitOff {
				settingstest.Override(t, "scheduler.submit_job", "false")
			}
			if err := checkSubmitFlag(tc.noSub); !errors.Is(err, tc.want) {
				t.Errorf("checkSubmitFlag = %v, want %v", err, tc.want)
			}
		})
	}
}

// A script that declares no resources submits with the default job settings, and one that declares them keeps its own.
func TestSubmitDefaultSpecs(t *testing.T) {
	settingstest.Override(t, "scheduler.ncpu", "3")
	settingstest.Override(t, "scheduler.mem", "6g")
	settingstest.Override(t, "scheduler.time", "45m")

	for name, in := range map[string]*scheduler.ScriptSpecs{
		"no specs":      nil,
		"no directives": {Spec: &scheduler.ResourceSpec{CpusPerTask: 1}},
		"empty specs":   {},
	} {
		got := submitDefaultSpecs(in)
		if !scheduler.HasSchedulerSpecs(got) || got.Spec == nil {
			t.Fatalf("%s: %+v is not a job", name, got)
		}
		if got.Spec.CpusPerTask != 3 || got.Spec.MemPerNodeMB != 6144 || got.Spec.Time != 45*time.Minute || got.Spec.Nodes != 1 || got.Spec.Ntasks != 1 {
			t.Errorf("%s: spec = %+v, want 3 CPUs, 6144 MB, 45m on one node", name, got.Spec)
		}
	}

	own := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{CpusPerTask: 8}}
	if got := submitDefaultSpecs(own); got != own || got.Spec.CpusPerTask != 8 {
		t.Errorf("a script with its own resources was changed: %+v", got.Spec)
	}
}

// A dependency has something to apply to once the run is submitted, so --submit in a job turns the check on.
func TestWillSubmitRunInAJob(t *testing.T) {
	specs := &scheduler.ScriptSpecs{HasDirectives: true}
	for _, tc := range []struct {
		name string
		sub  bool
		want bool
	}{
		{"in place", false, false},
		{"with --submit", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, true, false)
			setRunSubmit(t, tc.sub)
			useScheduler(t)
			if got := willSubmitRun(specs); got != tc.want {
				t.Errorf("willSubmitRun = %v, want %v", got, tc.want)
			}
		})
	}
}

// A submitted job runs `condatainer run` on the compute node, which is a job and must run in place.
// A job script that repeated --submit would submit again without end.
func TestSubmitIsNotForwardedToTheJob(t *testing.T) {
	fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
	fs.Bool("submit", false, "")
	fs.Bool("writable", false, "")
	if err := fs.Set("submit", "true"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("writable", "true"); err != nil {
		t.Fatal(err)
	}
	got := forwardedFlagArgs(fs)
	if len(got) != 1 || got[0] != "--writable='true'" {
		t.Errorf("forwarded %q, want only --writable", got)
	}
}
