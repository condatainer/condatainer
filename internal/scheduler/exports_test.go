package scheduler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An 8-CPU node running 4 ranks of 2 CPUs: the shape the PBS finding was about.
func hybridSpec() *ResourceSpec {
	return &ResourceSpec{Nodes: 2, Ntasks: 8, TasksPerNode: 4, CpusPerTask: 2, MemPerNodeMB: 16384}
}

func TestWriteResourceExports(t *testing.T) {
	var b strings.Builder
	writeResourceExports(&b, hybridSpec())
	got := b.String()
	for _, want := range []string{
		"export NNODE=2\n", "export CNT_NNODE=2\n",
		"export NRANK=8\n", "export CNT_NRANK=8\n",
		"export NRANK_PER_NODE=4\n", "export CNT_NRANK_PER_NODE=4\n",
		"export NCPU=2\n", "export CNT_NCPU=2\n",
		"export MEM=4096\n", "export CNT_MEM=4096\n",
		"export MEM_GB=4\n", "export CNT_MEM_GB=4\n",
		"export OMP_NUM_THREADS=2\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("exports missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "CNT_OMP_NUM_THREADS") {
		t.Errorf("OMP_NUM_THREADS has no CNT_ copy\n%s", got)
	}
}

// The Slurm, PBS and LSF job scripts export the same variables, from the one helper.
func TestScriptsExportResourceVars(t *testing.T) {
	writers := map[string]func(*JobSpec, string) (string, error){
		"slurm": func(j *JobSpec, d string) (string, error) { return newTestSlurmScheduler().CreateScriptWithSpec(j, d) },
		"pbs":   func(j *JobSpec, d string) (string, error) { return newTestPbsScheduler().CreateScriptWithSpec(j, d) },
		"lsf":   func(j *JobSpec, d string) (string, error) { return newTestLsfScheduler().CreateScriptWithSpec(j, d) },
	}

	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			jobSpec := &JobSpec{
				Name:     "exports",
				Command:  "echo hi",
				Specs:    &ScriptSpecs{Spec: hybridSpec(), RemainingFlags: []string{}},
				Metadata: map[string]string{},
			}
			path, err := write(jobSpec, t.TempDir())
			if err != nil {
				t.Fatalf("CreateScriptWithSpec: %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"export NCPU=2\n", "export CNT_NCPU=2\n", "export NRANK=8\n", "export OMP_NUM_THREADS=2\n"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("script missing %q\n%s", want, body)
				}
			}
			if strings.Contains(string(body), "OMP_NUM_THREADS:-") {
				t.Errorf("script still defaults OMP_NUM_THREADS\n%s", body)
			}
		})
	}
}

func TestHTCondorScriptExportsResourceVars(t *testing.T) {
	dir := t.TempDir()
	jobSpec := &JobSpec{
		Name:     "exports",
		Command:  "echo hi",
		Specs:    &ScriptSpecs{Spec: &ResourceSpec{CpusPerTask: 3}, RemainingFlags: []string{}},
		Metadata: map[string]string{},
	}
	if _, err := newTestHTCondorScheduler().CreateScriptWithSpec(jobSpec, dir); err != nil {
		t.Fatalf("CreateScriptWithSpec: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "exports.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"export NCPU=3\n", "export CNT_NCPU=3\n", "export NNODE=1\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("wrapper missing %q\n%s", want, body)
		}
	}
}

// An unparsed script has no spec to export.
func TestScriptsWithoutSpecExportNothing(t *testing.T) {
	jobSpec := &JobSpec{Name: "none", Command: "echo hi", Specs: &ScriptSpecs{RemainingFlags: []string{}}, Metadata: map[string]string{}}
	path, err := newTestPbsScheduler().CreateScriptWithSpec(jobSpec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "export CNT_") {
		t.Errorf("a script without a spec exports resource variables\n%s", body)
	}
}

func setExports(t *testing.T, rs *ResourceSpec) {
	t.Helper()
	clearJobEnvVars(t)
	for _, kv := range ResourceEnvVars(rs) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
}

func TestResourcesFromExports(t *testing.T) {
	t.Run("none set", func(t *testing.T) {
		clearJobEnvVars(t)
		if res := resourcesFromExports(); res != nil {
			t.Fatalf("expected nil, got %+v", res)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		setExports(t, hybridSpec())
		res := resourcesFromExports()
		if res == nil {
			t.Fatal("expected non-nil")
		}
		if res.Nodes != 2 || res.Ntasks != 8 || res.TasksPerNode != 4 || res.CpusPerTask != 2 {
			t.Errorf("geometry = %+v", res)
		}
		if got := res.GetMemPerTaskMB(); got != 4096 {
			t.Errorf("memory per rank = %d MB; want 4096", got)
		}
	})

	// The CNT_ copies are read. A user's own NCPU or MEM changes nothing.
	t.Run("user variables do not shadow", func(t *testing.T) {
		setExports(t, hybridSpec())
		t.Setenv("NCPU", "64")
		t.Setenv("MEM", "1")
		res := resourcesFromExports()
		if res.CpusPerTask != 2 || res.GetMemPerTaskMB() != 4096 {
			t.Errorf("CpusPerTask = %d, memory = %d MB; want 2 and 4096", res.CpusPerTask, res.GetMemPerTaskMB())
		}
	})

	t.Run("MPI variables stay ahead of CNT_NRANK", func(t *testing.T) {
		setExports(t, hybridSpec())
		t.Setenv("OMPI_COMM_WORLD_SIZE", "6")
		t.Setenv("OMPI_COMM_WORLD_LOCAL_SIZE", "3")
		res := resourcesFromExports()
		if res.Ntasks != 6 || res.TasksPerNode != 3 {
			t.Errorf("Ntasks = %d, TasksPerNode = %d; want 6 and 3", res.Ntasks, res.TasksPerNode)
		}
	})

	t.Run("no memory", func(t *testing.T) {
		setExports(t, &ResourceSpec{CpusPerTask: 4})
		res := resourcesFromExports()
		if res.CpusPerTask != 4 || res.GetMemPerTaskMB() != 0 {
			t.Errorf("res = %+v", res)
		}
	})
}

// Outside a scheduler job the exports are still what a nested run reads.
func TestCurrentJobResourcesPrefersExports(t *testing.T) {
	setExports(t, hybridSpec())
	t.Setenv("PBS_JOBID", "1.srv")
	t.Setenv("NCPUS", "8") // PBS's own value: the first chunk's CPUs
	res := CurrentJobResources()
	if res == nil || res.CpusPerTask != 2 {
		t.Fatalf("CurrentJobResources = %+v; want 2 CPUs per rank", res)
	}
}

func TestCurrentJobResourcesWithoutJob(t *testing.T) {
	clearJobEnvVars(t)
	if res := CurrentJobResources(); res != nil {
		t.Fatalf("expected nil outside a job, got %+v", res)
	}
}
