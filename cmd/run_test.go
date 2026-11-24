package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/runtime/hostrun"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/testenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// nopScheduler is an active scheduler that is never called.
type nopScheduler struct{ scheduler.Scheduler }

// setPlace makes the process look like it is in a job, in a container, both or neither.
// Every job variable of the real environment is cleared first, so a test sets what it needs.
func setPlace(t *testing.T, job, container bool) {
	t.Helper()
	testenv.ClearJobEnvFor(t)
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
		noScheduler bool
		submitOff   bool
		want        error
	}{
		{name: "flag off"},
		{name: "ready", sub: true},
		{name: "with --no-submit", sub: true, noSub: true, want: errSubmitWithNoSubmit},
		{name: "submission off", sub: true, submitOff: true, want: errSubmitNoScheduler},
		{name: "no scheduler", sub: true, noScheduler: true, want: errSubmitNoScheduler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, true, false)
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

// A run in place gets the allocation of its job. A submitted job is a new job and gets its own resources.
func TestEffectiveResourceSpecInAJob(t *testing.T) {
	specs := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{Ntasks: 4, CpusPerTask: 2}}
	for _, tc := range []struct {
		name        string
		sub         bool
		cpus, ranks int
	}{
		{"in place", false, 4, 1},
		{"with --submit", true, 2, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, true, false)
			setRunSubmit(t, tc.sub)
			t.Setenv("SLURM_NTASKS", "1")
			t.Setenv("CNT_NCPU", "4")
			got := effectiveResourceSpec(specs)
			if got.CpusPerTask != tc.cpus || got.GetNtasks() != tc.ranks {
				t.Errorf("got %d CPUs, %d ranks; want %d, %d", got.CpusPerTask, got.GetNtasks(), tc.cpus, tc.ranks)
			}
		})
	}
}

// jobEnv puts the process in a job with the given task count and the job's CPUs and memory, with no OMP_NUM_THREADS.
func jobEnv(t *testing.T, tasks string) {
	t.Helper()
	setPlace(t, true, false)
	t.Setenv("SLURM_NTASKS", tasks)
	t.Setenv("SLURM_CPUS_PER_TASK", "4")
	t.Setenv("CNT_NCPU", "4")
	t.Setenv("CNT_MEM", "16384")
	t.Setenv("OMP_NUM_THREADS", "")
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// A script in a one-task job gets the job's CPUs and memory and no rank variable. An OMP_NUM_THREADS that is set stays.
func TestResourceEnvInASingleTaskJob(t *testing.T) {
	jobEnv(t, "1")
	specs := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{CpusPerTask: 2, MemPerNodeMB: 1024}}
	for name, env := range map[string][]string{"run": resourceEnvSettings(specs), "run without a parsed spec": liveJobResourceEnvSettings()} {
		got := envMap(env)
		if got["NCPU"] != "4" || got["MEM"] != "16384" || got["OMP_NUM_THREADS"] != "4" {
			t.Errorf("%s: NCPU=%s MEM=%s OMP_NUM_THREADS=%s, want the job's 4, 16384, 4", name, got["NCPU"], got["MEM"], got["OMP_NUM_THREADS"])
		}
		for _, k := range []string{"NRANK", "NNODE", "NRANK_PER_NODE", "CNT_NRANK"} {
			if _, ok := got[k]; ok {
				t.Errorf("%s sets %s", name, k)
			}
		}
	}

	t.Setenv("OMP_NUM_THREADS", "16")
	if _, ok := envMap(resourceEnvSettings(specs))["OMP_NUM_THREADS"]; ok {
		t.Error("an OMP_NUM_THREADS that is set was replaced")
	}
}

// In a job a run sets the job's size every time, so NCPU equals CNT_NCPU. Outside a job the script and the flags decide.
func TestResourceEnvInAJobIsTheJobs(t *testing.T) {
	specs := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{CpusPerTask: 3, MemPerNodeMB: 1024}}

	t.Run("a job a job script started", func(t *testing.T) {
		jobEnv(t, "1")        // CNT_NCPU=4 and CNT_MEM=16384, as the header exports them
		t.Setenv("NCPU", "2") // a value of the caller's own
		got := envMap(resourceEnvSettings(specs))
		if got["NCPU"] != "4" || got["CNT_NCPU"] != "4" || got["MEM"] != "16384" || got["MEM_GB"] != "16" {
			t.Errorf("got %v, want the job's 4 CPUs and 16384 MB over the caller's NCPU=2", got)
		}
	})

	t.Run("a shell no job script started", func(t *testing.T) {
		jobEnv(t, "1")
		for _, k := range []string{"CNT_NCPU", "CNT_MEM"} {
			t.Setenv(k, "")
			os.Unsetenv(k) // only the scheduler describes the job
		}
		scheduler.SetActiveScheduler(allocationStub{res: &scheduler.ResourceSpec{CpusPerTask: 4, MemPerNodeMB: 16384}})
		t.Cleanup(scheduler.ClearActiveScheduler)

		got := envMap(resourceEnvSettings(specs))
		if got["NCPU"] != "4" || got["MEM"] != "16384" || got["MEM_GB"] != "16" || got["OMP_NUM_THREADS"] != "4" {
			t.Errorf("got %v, want the allocation's 4 CPUs and 16384 MB, and 4 threads", got)
		}
	})

	t.Run("no job", func(t *testing.T) {
		setPlace(t, false, false)
		t.Setenv("NCPU", "2")
		if got := envMap(resourceEnvSettings(specs))["NCPU"]; got != "3" {
			t.Errorf("NCPU = %q, want the script's 3 over the 2 in the environment", got)
		}
	})
}

// A job with more than one rank passes through: only an unset OMP_NUM_THREADS is filled, and the script's spec stands.
func TestResourceEnvInAMultiTaskJob(t *testing.T) {
	jobEnv(t, "4")
	specs := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{Ntasks: 4, CpusPerTask: 2}}
	for name, env := range map[string][]string{"run": resourceEnvSettings(specs), "run without a parsed spec": resourceEnvSettings(&scheduler.ScriptSpecs{HasDirectives: true})} {
		if got := strings.Join(env, " "); got != "OMP_NUM_THREADS=4" {
			t.Errorf("%s sets %q, want only OMP_NUM_THREADS=4", name, got)
		}
	}
	if got := effectiveResourceSpec(specs); got.CpusPerTask != 2 || got.GetNtasks() != 4 {
		t.Errorf("the job was read: %d CPUs, %d ranks; want the script's 2 and 4", got.CpusPerTask, got.GetNtasks())
	}

	t.Setenv("OMP_NUM_THREADS", "8")
	if env := resourceEnvSettings(specs); len(env) != 0 {
		t.Errorf("sets %v with OMP_NUM_THREADS already set", env)
	}
}

// -c replaces an OMP_NUM_THREADS that is set, outside a job. In a job -c has no effect.
func TestResourceEnvThreadsFlag(t *testing.T) {
	old := runCPU
	runCPU = 3
	t.Cleanup(func() { runCPU = old })
	specs := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{CpusPerTask: 3}}

	setPlace(t, false, false)
	t.Setenv("OMP_NUM_THREADS", "16")
	if got := envMap(resourceEnvSettings(specs))["OMP_NUM_THREADS"]; got != "3" {
		t.Errorf("outside a job: OMP_NUM_THREADS = %q, want 3", got)
	}

	jobEnv(t, "1")
	t.Setenv("OMP_NUM_THREADS", "16")
	if _, ok := envMap(resourceEnvSettings(specs))["OMP_NUM_THREADS"]; ok {
		t.Error("in a job: -c replaced OMP_NUM_THREADS")
	}
}

// A script with no resources runs with the config defaults, and the resource flags apply to them.
func TestEffectiveResourceSpecOfAScriptWithNoResources(t *testing.T) {
	setPlace(t, false, false)
	settingstest.Override(t, "scheduler.ncpu", "3")
	settingstest.Override(t, "scheduler.mem", "6g")
	path := filepath.Join(t.TempDir(), "plain.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	specs, err := scheduler.ReadScriptSpecsFor(path, scheduler.Subjects{})
	if err != nil {
		t.Fatal(err)
	}
	if got := effectiveResourceSpec(specs); got.CpusPerTask != 3 || got.GetMemPerTaskMB() != 6144 {
		t.Errorf("defaults: %d CPUs, %d MB; want the config's 3 and 6144", got.CpusPerTask, got.GetMemPerTaskMB())
	}
	specs.Spec.Override(&scheduler.ResourceSpec{CpusPerTask: 8, MemPerCpuMB: 1024})
	if got := effectiveResourceSpec(specs); got.CpusPerTask != 8 || got.GetMemPerTaskMB() != 8192 {
		t.Errorf("with -c 8 and -m 8G: %d CPUs, %d MB; want 8 and 8192", got.CpusPerTask, got.GetMemPerTaskMB())
	}
}

// resourceFlagSet binds the flags to the run globals and sets the ones given, as typed.
func resourceFlagSet(t *testing.T, typed map[string]string) *pflag.FlagSet {
	t.Helper()
	oldCPU, oldMem, oldTime, oldGPU := runCPU, runMem, runTime, runGPU
	t.Cleanup(func() { runCPU, runMem, runTime, runGPU = oldCPU, oldMem, oldTime, oldGPU })
	runCPU, runMem, runTime, runGPU = 0, "", "", ""
	fs := pflag.NewFlagSet("", pflag.ContinueOnError)
	fs.IntVar(&runCPU, "cpu", 0, "")
	fs.StringVar(&runMem, "mem", "", "")
	fs.StringVar(&runTime, "time", "", "")
	fs.StringVar(&runGPU, "gpu", "", "")
	for name, value := range typed {
		if err := fs.Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

// The resource flags are read and checked when typed, whether or not they will have an effect.
func TestParseResourceFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typed   map[string]string
		wantErr string
	}{
		{"none", nil, ""},
		{"all four", map[string]string{"cpu": "4", "mem": "8G", "time": "2h", "gpu": "a100:2"}, ""},
		{"zero CPUs", map[string]string{"cpu": "0"}, "--cpu 0: must be at least 1"},
		{"negative CPUs", map[string]string{"cpu": "-2"}, "--cpu -2: must be at least 1"},
		{"bad memory", map[string]string{"mem": "lots"}, "--mem"},
		{"bad time", map[string]string{"time": "soon"}, "--time"},
		{"bad GPU", map[string]string{"gpu": "x:y:z"}, "--gpu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseResourceFlags(resourceFlagSet(t, tc.typed))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one with %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.typed == nil && (f.cpus != 0 || f.memMB != 0 || f.time != 0 || f.gpu != nil) {
				t.Errorf("flags that were not typed are not zero: %+v", f)
			}
			if tc.typed != nil && (f.cpus != 4 || f.memMB != 8192 || f.time != 2*time.Hour || f.gpu == nil || f.gpu.Count != 2) {
				t.Errorf("parsed %+v", f)
			}
		})
	}
}

// -m is memory per rank. One rank gets it as the memory of its node, exactly. More ranks get it per CPU.
func TestResourceFlagsOverride(t *testing.T) {
	oneRank := &scheduler.ResourceSpec{Nodes: 1, CpusPerTask: 4}
	fourRanks := &scheduler.ResourceSpec{Ntasks: 4, TasksPerNode: 4, CpusPerTask: 2}
	mem := resourceFlags{memMB: 5120}

	if got := mem.override(oneRank); got.MemPerNodeMB != 5120 || got.MemPerCpuMB != 0 || got.CpusPerTask != 0 {
		t.Errorf("-m 5G, one rank: %+v, want 5120 MB for the node and the CPUs left alone", got)
	}
	both := resourceFlags{cpus: 6, memMB: 5120}
	if got := both.override(oneRank); got.MemPerNodeMB != 5120 || got.MemPerCpuMB != 0 || got.CpusPerTask != 6 {
		t.Errorf("-c 6 -m 5G, one rank: %+v, want exactly 5120 MB whatever -c is", got)
	}
	if got := (resourceFlags{memMB: 4096}).override(fourRanks); got.MemPerCpuMB != 2048 || got.MemPerNodeMB != 0 || got.CpusPerTask != 0 {
		t.Errorf("-m 4G, four ranks of 2 CPUs: %+v, want 2048 MB per CPU", got)
	}
	if got := (resourceFlags{cpus: 8, memMB: 4096}).override(fourRanks); got.MemPerCpuMB != 512 || got.CpusPerTask != 8 {
		t.Errorf("-c 8 -m 4G, four ranks: %+v, want 512 MB per CPU on 8 CPUs", got)
	}
	if got := (resourceFlags{}).override(oneRank); got.MemPerNodeMB != 0 || got.MemPerCpuMB != 0 || got.CpusPerTask != 0 || got.Time != 0 || got.Gpu != nil {
		t.Errorf("no flags: %+v, want nothing set", got)
	}
}

// The memory of a one-rank script is the request, whichever way the script gave its own.
func TestMemoryFlagReplacesTheScriptsMemory(t *testing.T) {
	for name, script := range map[string]*scheduler.ResourceSpec{
		"per node": {Nodes: 1, CpusPerTask: 4, MemPerNodeMB: 2048},
		"per CPU":  {Nodes: 1, CpusPerTask: 4, MemPerCpuMB: 1024},
	} {
		rs := *script
		rs.Override(resourceFlags{cpus: 6, memMB: 5120}.override(&rs))
		if got := rs.GetMemPerTaskMB(); got != 5120 || rs.CpusPerTask != 6 {
			t.Errorf("%s: %d CPUs, %d MB, want 6 and 5120", name, rs.CpusPerTask, got)
		}
	}
}

// A job keeps the resources it was given, unless --submit starts a new one.
func TestJobKeepsResources(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		job, container, submit bool
		want                   bool
	}{
		{"no job", false, false, false, false},
		{"container, no job", false, true, false, false},
		{"job", true, false, false, true},
		{"job in a container", true, true, false, true},
		{"job with --submit", true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, tc.job, tc.container)
			setRunSubmit(t, tc.submit)
			if got := jobKeepsResources(); got != tc.want {
				t.Errorf("jobKeepsResources = %v, want %v", got, tc.want)
			}
		})
	}
}

// The warning names the resource flags that were typed, and says nothing when none was.
func TestWarnResourceFlagsInAJob(t *testing.T) {
	newFlags := func(typed ...string) *pflag.FlagSet {
		fs := pflag.NewFlagSet("", pflag.ContinueOnError)
		fs.Int("cpu", 0, "")
		fs.String("mem", "", "")
		fs.String("time", "", "")
		fs.String("gpu", "", "")
		fs.String("partition", "", "")
		for _, name := range typed {
			if err := fs.Set(name, "1"); err != nil {
				t.Fatal(err)
			}
		}
		return fs
	}
	out, _ := stderrOf(t, func() error { warnResourceFlagsInJob(newFlags("cpu", "time", "partition")); return nil })
	if !strings.Contains(out, "--cpu, --time") || strings.Contains(out, "partition") {
		t.Errorf("warning = %q, want --cpu and --time, and no partition", out)
	}
	if out, _ := stderrOf(t, func() error { warnResourceFlagsInJob(newFlags("partition")); return nil }); out != "" {
		t.Errorf("a flag that is not a resource flag warned: %q", out)
	}
}

// htcondorStub is an active scheduler that says it is HTCondor.
type htcondorStub struct{ nopScheduler }

func (htcondorStub) GetType() scheduler.SchedulerType { return scheduler.SchedulerHTCondor }

// An HTCondor job is one slot on one machine, so a submit of more than one rank is refused there and only there.
func TestRunRefusalsOfMoreThanOneRank(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mpiexec"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	specs := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{Ntasks: 4, CpusPerTask: 2}}
	for _, tc := range []struct {
		name  string
		sched scheduler.Scheduler
		want  []string
	}{
		{"HTCondor", htcondorStub{}, []string{"HTCondor runs no job with more than one rank (4 ranks)"}},
		{"SLURM", slurmStub{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, false, false)
			useScheduler(t)
			scheduler.SetActiveScheduler(tc.sched)
			if got := runRefusals(specs, nil); !slices.Equal(got, tc.want) {
				t.Errorf("runRefusals = %q, want %q", got, tc.want)
			}
		})
	}
}

// slurmStub is an active scheduler that says what it is and reports no job resources.
type slurmStub struct{ nopScheduler }

func (slurmStub) GetType() scheduler.SchedulerType { return scheduler.SchedulerSLURM }

func (slurmStub) GetJobResources() *scheduler.ResourceSpec { return nil }

// The dry run lists a time only for a submitted job. A run neither enforces nor exports one.
func TestDryRunTimeLine(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "job.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\n#NCPU: 2\n#TIME: 3h\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "base.sqf")
	if err := os.WriteFile(base, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	oldBase := runBaseImage
	runBaseImage = base
	t.Cleanup(func() { runBaseImage = oldBase })

	for _, tc := range []struct {
		name                   string
		job, submit, scheduler bool
		want                   bool
	}{
		{"login node with a scheduler", false, false, true, true},
		{"no scheduler", false, false, false, false},
		{"in a job", true, false, true, false},
		{"--submit in a job", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, tc.job, false)
			setRunSubmit(t, tc.submit)
			specs, err := scheduler.ReadScriptSpecsFor(script, scheduler.Subjects{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.scheduler {
				useScheduler(t)
				scheduler.SetActiveScheduler(slurmStub{})
			}
			out := captureFile(t, &os.Stdout, func() {
				printDryRunSummary(context.Background(), script, script, specs, nil, nil, nil)
			})
			if got := strings.Contains(out, "Time:       03:00:00"); got != tc.want {
				t.Errorf("Time line shown = %v, want %v\n%s", got, tc.want, out)
			}
			if !strings.Contains(out, "CPU:        2") {
				t.Errorf("the resources are missing from\n%s", out)
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

// The flags set on the command line go to the host as arguments, except the ones left out.
func TestFlagArgv(t *testing.T) {
	fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
	fs.Bool("submit", false, "")
	fs.String("submit-on", "", "")
	fs.String("afterok", "", "")
	fs.StringSlice("bind", nil, "")
	fs.Int("cpu", 0, "")
	for _, kv := range [][2]string{{"submit", "true"}, {"submit-on", "login"}, {"afterok", "12:13"}, {"bind", "/a b"}, {"bind", "/c"}} {
		if err := fs.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	got := flagArgv(fs, map[string]bool{"submit-on": true})
	want := []string{"--afterok=12:13", "--bind=/a b", "--bind=/c", "--submit=true"}
	if !slices.Equal(got, want) {
		t.Errorf("flagArgv = %q, want %q", got, want)
	}
}

// Inside a container, run --submit sends the same command to the host, with --submit-on as the node.
func TestSubmitThroughHostSendsTheCommand(t *testing.T) {
	setRunSubmit(t, true)
	old := runSubmitOn
	runSubmitOn = "login"
	t.Cleanup(func() { runSubmitOn = old })
	t.Setenv(hostrun.EnvSock, "/.condatainer/host/sock")

	var sent hostrun.Call
	oldCall := hostRunCall
	hostRunCall = func(_ context.Context, call hostrun.Call) (int, error) { sent = call; return 0, nil }
	t.Cleanup(func() { hostRunCall = oldCall })

	c := &cobra.Command{Use: "run"}
	c.Flags().SetInterspersed(false)
	c.Flags().Bool("submit", false, "")
	c.Flags().String("submit-on", "", "")
	c.Flags().String("afterok", "", "")
	if err := c.ParseFlags([]string{"--submit", "--submit-on=login", "--afterok", "12", "step.sh", "--submit-on", "x"}); err != nil {
		t.Fatal(err)
	}
	if err := submitThroughHost(c, c.Flags().Args()); err != nil {
		t.Fatal(err)
	}
	want := []string{"condatainer", "run", "--afterok=12", "--submit=true", "step.sh", "--submit-on", "x"}
	if !slices.Equal(sent.Argv, want) {
		t.Errorf("sent %q, want %q", sent.Argv, want)
	}
	if sent.On != "login" || sent.Sock != "/.condatainer/host/sock" {
		t.Errorf("on %q, socket %q", sent.On, sent.Sock)
	}
	if cwd, _ := os.Getwd(); sent.Cwd != cwd {
		t.Errorf("working directory %q, want %q", sent.Cwd, cwd)
	}
}

// A container with no host-run says so, naming host-run.
func TestSubmitThroughHostWithNoRelayIsAnError(t *testing.T) {
	oldCall := hostRunCall
	hostRunCall = func(context.Context, hostrun.Call) (int, error) { return 0, hostrun.ErrNoRelay }
	t.Cleanup(func() { hostRunCall = oldCall })

	err := submitThroughHost(&cobra.Command{Use: "run"}, []string{"step.sh"})
	if !errors.Is(err, hostrun.ErrNoRelay) || !strings.Contains(err.Error(), "host-run") {
		t.Errorf("err = %v, want one that wraps ErrNoRelay and names host-run", err)
	}
}

// Two runs of one script in the same second write their job files under two names, base and base-2.
func TestCreateRunJobScriptTakesTheNextFreeName(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, sched := range []func() (scheduler.Scheduler, error){
		func() (scheduler.Scheduler, error) { return scheduler.NewSlurmSchedulerWithBinary(exe) },
		func() (scheduler.Scheduler, error) { return scheduler.NewHTCondorSchedulerWithBinary(exe) },
	} {
		s, err := sched()
		if err != nil {
			t.Fatal(err)
		}
		t.Run(string(s.GetType()), func(t *testing.T) {
			dir := t.TempDir()
			var logs []string
			for _, want := range []string{"job_20261124_043410", "job_20261124_043410-2"} {
				specs := &scheduler.ScriptSpecs{Spec: &scheduler.ResourceSpec{Nodes: 1, CpusPerTask: 1}}
				jobSpec := &scheduler.JobSpec{Command: "true", Specs: specs, UniqueName: true}
				_, name, err := createRunJobScript(func(name string) (string, error) {
					jobSpec.Name = name
					return s.CreateScriptWithSpec(jobSpec, dir)
				}, "job_20261124_043410", &specs.Control)
				if err != nil || name != want {
					t.Fatalf("name = %q, %v; want %q", name, err, want)
				}
				logs = append(logs, specs.Control.Stdout)
			}
			if logs[0] == logs[1] || !strings.HasSuffix(logs[1], "job_20261124_043410-2.log") {
				t.Errorf("logs = %q", logs)
			}
		})
	}
}

// A run in a one-task job warns when the job does not report its memory, since MEM is then the script's or the setting's.
func TestMemoryGuessWarning(t *testing.T) {
	specs := &scheduler.ScriptSpecs{HasDirectives: true, Spec: &scheduler.ResourceSpec{CpusPerTask: 3, MemPerNodeMB: 1024}}
	for _, tc := range []struct {
		name string
		job  bool
		env  map[string]string
		res  *scheduler.ResourceSpec // what the scheduler reports
		want bool
	}{
		{"no job", false, nil, nil, false},
		{"a job a job script started", true, map[string]string{"CNT_NCPU": "4", "CNT_MEM": "16384"}, nil, false},
		{"a job whose scheduler reports memory", true, nil, &scheduler.ResourceSpec{CpusPerTask: 4, MemPerNodeMB: 8192}, false},
		{"a job whose scheduler reports no memory", true, nil, &scheduler.ResourceSpec{CpusPerTask: 4}, true},
		{"a job with more than one rank", true, map[string]string{"SLURM_NTASKS": "4"}, &scheduler.ResourceSpec{CpusPerTask: 4}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, tc.job, false)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			scheduler.SetActiveScheduler(allocationStub{res: tc.res})
			t.Cleanup(scheduler.ClearActiveScheduler)
			if got := memoryGuessWarning(specs) != ""; got != tc.want {
				t.Errorf("warns = %v, want %v", got, tc.want)
			}
		})
	}
}
