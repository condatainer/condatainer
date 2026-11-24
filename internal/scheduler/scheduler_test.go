package scheduler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

func TestSpecDefaultsInitialValues(t *testing.T) {
	defaults := GetSpecDefaults()
	if defaults.CpusPerTask != 1 {
		t.Errorf("CpusPerTask = %d; want 1", defaults.CpusPerTask)
	}
	if defaults.Nodes != 0 {
		t.Errorf("Nodes = %d; want 0", defaults.Nodes)
	}
	if defaults.TasksPerNode != 0 {
		t.Errorf("TasksPerNode = %d; want 0", defaults.TasksPerNode)
	}
	if defaults.MemPerNodeMB != 2048 {
		t.Errorf("MemPerNodeMB = %d; want 2048", defaults.MemPerNodeMB)
	}
	if defaults.Time != 2*time.Hour {
		t.Errorf("Time = %v; want 2h", defaults.Time)
	}
}

// Memory is one value, per CPU or per node: whichever Override sets replaces the one that was there.
func TestOverrideMemoryReplacesTheOtherForm(t *testing.T) {
	for _, tc := range []struct {
		name  string
		base  ResourceSpec
		other ResourceSpec
		want  int64 // MB per task
	}{
		{"per-node over per-CPU", ResourceSpec{CpusPerTask: 4, MemPerCpuMB: 1024}, ResourceSpec{MemPerNodeMB: 16384}, 16384},
		{"per-CPU over per-node", ResourceSpec{CpusPerTask: 4, MemPerNodeMB: 16384}, ResourceSpec{MemPerCpuMB: 1024}, 4096},
		{"per-node over per-node", ResourceSpec{CpusPerTask: 4, MemPerNodeMB: 2048}, ResourceSpec{MemPerNodeMB: 8192}, 8192},
		{"no memory in other", ResourceSpec{CpusPerTask: 4, MemPerCpuMB: 1024}, ResourceSpec{CpusPerTask: 2}, 2048},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := tc.base
			rs.Override(&tc.other)
			if got := rs.GetMemPerTaskMB(); got != tc.want {
				t.Errorf("memory per task = %d MB, want %d", got, tc.want)
			}
		})
	}
}

// shape is one common way a job lays out its tasks.
type shape struct {
	name  string
	rs    ResourceSpec // geometry only, no memory
	ranks int
	nodes int
}

var shapes = []shape{
	{"serial", ResourceSpec{Nodes: 1, TasksPerNode: 1, CpusPerTask: 1}, 1, 1},
	{"threaded", ResourceSpec{Nodes: 1, TasksPerNode: 1, CpusPerTask: 8}, 1, 1},
	{"MPI on one node", ResourceSpec{Nodes: 1, Ntasks: 4, TasksPerNode: 4, CpusPerTask: 1}, 4, 1},
	{"MPI on two nodes", ResourceSpec{Nodes: 2, Ntasks: 8, TasksPerNode: 4, CpusPerTask: 1}, 8, 2},
	{"hybrid MPI and threads", ResourceSpec{Nodes: 2, Ntasks: 4, TasksPerNode: 2, CpusPerTask: 8}, 4, 2},
	{"MPI with free placement", ResourceSpec{Ntasks: 6, CpusPerTask: 1}, 6, 1},
	{"MPI over an uneven split", ResourceSpec{Nodes: 3, Ntasks: 10, TasksPerNode: 4, CpusPerTask: 1}, 10, 3},
}

// memory is a way to ask for RAM, with what each rank gets on a shape.
type memory struct {
	name    string
	apply   func(*ResourceSpec)
	perRank func(rs ResourceSpec) int64
}

var memories = []memory{
	{"none", func(*ResourceSpec) {}, func(ResourceSpec) int64 { return 0 }},
	{
		"per CPU",
		func(rs *ResourceSpec) { rs.MemPerCpuMB = 1024 },
		func(rs ResourceSpec) int64 { return 1024 * int64(rs.CpusPerTask) },
	},
	{
		"per node",
		func(rs *ResourceSpec) { rs.MemPerNodeMB = 16384 },
		func(rs ResourceSpec) int64 { return 16384 / int64(max(rs.TasksPerNode, 1)) },
	},
}

func eachShape(t *testing.T, fn func(t *testing.T, s shape, m memory, rs ResourceSpec)) {
	t.Helper()
	for _, s := range shapes {
		for _, m := range memories {
			rs := s.rs
			m.apply(&rs)
			t.Run(s.name+"/"+m.name, func(t *testing.T) { fn(t, s, m, rs) })
		}
	}
}

// The ranks and the memory of a rank follow from the geometry, whichever way memory is asked for.
func TestShapeAccessors(t *testing.T) {
	eachShape(t, func(t *testing.T, s shape, m memory, rs ResourceSpec) {
		if got := rs.GetNtasks(); got != s.ranks {
			t.Errorf("ranks = %d, want %d", got, s.ranks)
		}
		if got, want := rs.IsMPI(), s.ranks > 1; got != want {
			t.Errorf("IsMPI = %v, want %v", got, want)
		}
		if got, want := rs.IsOpenMP(), rs.CpusPerTask > 1; got != want {
			t.Errorf("IsOpenMP = %v, want %v", got, want)
		}
		if got, want := rs.GetMemPerTaskMB(), m.perRank(rs); got != want {
			t.Errorf("memory per rank = %d MB, want %d", got, want)
		}
	})
}

// A job's variables read back as the CPUs and the memory of a rank. The layout is not read.
func TestShapeExportsRoundTrip(t *testing.T) {
	eachShape(t, func(t *testing.T, s shape, m memory, rs ResourceSpec) {
		setExports(t, &rs)
		got := resourcesFromExports()
		if got == nil {
			t.Fatal("no resources read back")
		}
		if got.CpusPerTask != rs.CpusPerTask {
			t.Errorf("CPUs = %d, want %d", got.CpusPerTask, rs.CpusPerTask)
		}
		if g, w := got.GetMemPerTaskMB(), rs.GetMemPerTaskMB(); g != w {
			t.Errorf("memory per rank = %d MB, want %d", g, w)
		}
		if got.Nodes != 0 || got.Ntasks != 0 || got.TasksPerNode != 0 {
			t.Errorf("read a layout, %d nodes, %d ranks, %d per node; want none", got.Nodes, got.Ntasks, got.TasksPerNode)
		}
	})
}

// A script run in a job gets the job's layout, and a field the job does not give stays the script's.
func TestShapeOverlayOfAJob(t *testing.T) {
	jobs := []struct {
		name string
		job  ResourceSpec
	}{
		{"serial job", ResourceSpec{Nodes: 1, Ntasks: 1, TasksPerNode: 1, CpusPerTask: 4, MemPerNodeMB: 16384}},
		{"MPI job", ResourceSpec{Nodes: 2, Ntasks: 8, TasksPerNode: 4, CpusPerTask: 1, MemPerNodeMB: 32768}},
	}
	for _, j := range jobs {
		for _, s := range shapes {
			for _, m := range memories {
				script := s.rs
				m.apply(&script)
				t.Run(j.name+"/script "+s.name+"/"+m.name, func(t *testing.T) {
					job := j.job
					got := ResolveResourceSpecFrom(ResourceSpec{}, &job, &ScriptSpecs{HasDirectives: true, Spec: &script})
					if got.GetNtasks() != job.Ntasks || got.CpusPerTask != job.CpusPerTask || got.Nodes != job.Nodes {
						t.Errorf("layout = %d ranks, %d CPUs, %d nodes; want the job's %d, %d, %d",
							got.GetNtasks(), got.CpusPerTask, got.Nodes, job.Ntasks, job.CpusPerTask, job.Nodes)
					}
					if g, w := got.GetMemPerTaskMB(), job.GetMemPerTaskMB(); g != w {
						t.Errorf("memory per rank = %d MB, want the job's %d", g, w)
					}
				})
			}
		}
	}
}

// The exported variables describe one rank: its CPUs, its threads and its memory, never the whole job's.
func TestShapeEnvIsPerRank(t *testing.T) {
	eachShape(t, func(t *testing.T, s shape, m memory, rs ResourceSpec) {
		env := map[string]string{}
		for _, kv := range ResourceEnvVars(&rs) {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
		want := map[string]string{
			"NRANK": strconv.Itoa(s.ranks),
			"NNODE": strconv.Itoa(s.nodes),
			"NCPU":  strconv.Itoa(rs.CpusPerTask),
		}
		if perRank := m.perRank(rs); perRank > 0 {
			want["MEM"] = strconv.FormatInt(perRank, 10)
		} else if _, set := env["MEM"]; set {
			t.Errorf("MEM = %s with no memory asked for", env["MEM"])
		}
		for k, v := range want {
			if env[k] != v {
				t.Errorf("%s = %q, want %q", k, env[k], v)
			}
		}
		if v, ok := env["OMP_NUM_THREADS"]; ok {
			t.Errorf("OMP_NUM_THREADS = %q is set; only a run sets it", v)
		}
	})
}

// What a script does not set comes from the scheduler.ncpu, scheduler.mem and scheduler.time settings, however the script is written.
func TestScriptsTakeMissingResourcesFromTheConfig(t *testing.T) {
	settingstest.Override(t, "scheduler.ncpu", "3")
	settingstest.Override(t, "scheduler.mem", "6g")
	settingstest.Override(t, "scheduler.time", "45m")

	for _, tc := range []struct {
		name       string
		script     string
		directives bool
		cpus       int
		memMB      int64
		time       time.Duration
	}{
		{"no resources", "", false, 3, 6144, 45 * time.Minute},
		{"only #NCPU", "#NCPU: 5\n", true, 5, 6144, 45 * time.Minute},
		{"only #MEM", "#MEM: 8G\n", true, 3, 8192, 45 * time.Minute},
		{"only #TIME", "#TIME: 3h\n", true, 3, 6144, 3 * time.Hour},
		{"native SLURM, only a time", "#SBATCH --time=01:00:00\n", true, 3, 6144, time.Hour},
		{"native SLURM, only CPUs", "#SBATCH --cpus-per-task=2\n", true, 2, 6144, 45 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "job.sh")
			if err := os.WriteFile(path, []byte("#!/bin/bash\n"+tc.script+"true\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			specs, err := ReadScriptSpecsFor(path, Subjects{})
			if err != nil {
				t.Fatal(err)
			}
			if specs.HasDirectives != tc.directives || specs.Spec == nil {
				t.Fatalf("HasDirectives = %v, Spec = %v", specs.HasDirectives, specs.Spec)
			}
			rs := specs.Spec
			if rs.CpusPerTask != tc.cpus || rs.GetMemPerTaskMB() != tc.memMB || rs.Time != tc.time {
				t.Errorf("got %d CPUs, %d MB, %v; want %d, %d, %v", rs.CpusPerTask, rs.GetMemPerTaskMB(), rs.Time, tc.cpus, tc.memMB, tc.time)
			}
		})
	}
}
