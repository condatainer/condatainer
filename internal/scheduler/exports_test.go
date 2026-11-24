package scheduler

import (
	"os"
	"path/filepath"
	"slices"
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
	} {
		if !strings.Contains(got, want) {
			t.Errorf("exports missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "OMP_NUM_THREADS") {
		t.Errorf("a job script exports OMP_NUM_THREADS, which the run in the job sets when it is unset\n%s", got)
	}
}

// The unset line names every variable the exports can write, and comes before them.
func TestWriteResourceExportsUnsetsFirst(t *testing.T) {
	var b strings.Builder
	writeResourceExports(&b, hybridSpec())
	got := b.String()
	line, _, _ := strings.Cut(got, "\n")
	if !strings.HasPrefix(line, "unset ") {
		t.Fatalf("the first line is %q, want an unset line", line)
	}
	unset := strings.Fields(line)[1:]
	for _, kv := range ResourceEnvVars(hybridSpec()) {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(unset, name) {
			t.Errorf("%s is exported but not unset", name)
		}
		if strings.Index(got, "export "+kv) < len(line) {
			t.Errorf("%s is exported before the unset line", name)
		}
	}
	if slices.Contains(unset, "OMP_NUM_THREADS") {
		t.Error("OMP_NUM_THREADS is unset, which the scheduler may have set for the job")
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
			for _, want := range []string{"export NCPU=2\n", "export CNT_NCPU=2\n", "export NRANK=8\n"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("script missing %q\n%s", want, body)
				}
			}
			if strings.Contains(string(body), "OMP_NUM_THREADS") {
				t.Errorf("script sets OMP_NUM_THREADS\n%s", body)
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
func TestScriptsWithoutSpecExportNoResources(t *testing.T) {
	jobSpec := &JobSpec{Name: "none", Command: "echo hi", Specs: &ScriptSpecs{RemainingFlags: []string{}}, Metadata: map[string]string{}}
	path, err := newTestPbsScheduler().CreateScriptWithSpec(jobSpec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "export CNT_NCPU") {
		t.Errorf("a script without a spec exports resource variables\n%s", body)
	}
}

// unsetLoginNode leaves CNT_LOGIN_NODE unset for the test.
func unsetLoginNode(t *testing.T) {
	t.Helper()
	t.Setenv(LoginNodeVar, "")
	os.Unsetenv(LoginNodeVar)
}

func TestLoginNode(t *testing.T) {
	t.Run("a recorded value wins", func(t *testing.T) {
		clearJobEnvVars(t)
		t.Setenv(LoginNodeVar, "login02")
		if got := LoginNode(); got != "login02" {
			t.Errorf("LoginNode = %q, want login02", got)
		}
	})
	t.Run("a recorded value wins in a job", func(t *testing.T) {
		clearJobEnvVars(t)
		t.Setenv("SLURM_JOB_ID", "1")
		t.Setenv(LoginNodeVar, "login02")
		if got := LoginNode(); got != "login02" {
			t.Errorf("LoginNode = %q, want login02", got)
		}
	})
	t.Run("a value that is not a host name is ignored", func(t *testing.T) {
		clearJobEnvVars(t)
		t.Setenv("SLURM_JOB_ID", "1")
		t.Setenv(LoginNodeVar, "a b;c")
		if got := LoginNode(); got != "" {
			t.Errorf("LoginNode = %q, want none", got)
		}
	})
	t.Run("outside a job it is this node", func(t *testing.T) {
		clearJobEnvVars(t)
		unsetLoginNode(t)
		host, err := os.Hostname()
		if err != nil || validateNodeName(host) != nil {
			t.Skipf("hostname %q is not a plain host name", host)
		}
		if got := LoginNode(); got != host {
			t.Errorf("LoginNode = %q, want %q", got, host)
		}
	})
	t.Run("in a job with none recorded it is empty", func(t *testing.T) {
		clearJobEnvVars(t)
		t.Setenv("SLURM_JOB_ID", "1")
		unsetLoginNode(t)
		if got := LoginNode(); got != "" {
			t.Errorf("LoginNode = %q, want none", got)
		}
	})
}

// Every scheduler's job script records the login node, whether or not it has a resource spec.
func TestScriptsExportTheLoginNode(t *testing.T) {
	writers := map[string]func(*JobSpec, string) (string, error){
		"slurm": func(j *JobSpec, d string) (string, error) { return newTestSlurmScheduler().CreateScriptWithSpec(j, d) },
		"pbs":   func(j *JobSpec, d string) (string, error) { return newTestPbsScheduler().CreateScriptWithSpec(j, d) },
		"lsf":   func(j *JobSpec, d string) (string, error) { return newTestLsfScheduler().CreateScriptWithSpec(j, d) },
		"htcondor": func(j *JobSpec, d string) (string, error) {
			if _, err := newTestHTCondorScheduler().CreateScriptWithSpec(j, d); err != nil {
				return "", err
			}
			return filepath.Join(d, j.Name+".sh"), nil
		},
	}
	specs := map[string]*ScriptSpecs{
		"with a spec":    {Spec: hybridSpec(), RemainingFlags: []string{}},
		"without a spec": {RemainingFlags: []string{}},
	}
	for name, write := range writers {
		for kind, spec := range specs {
			t.Run(name+" "+kind, func(t *testing.T) {
				clearJobEnvVars(t)
				t.Setenv(LoginNodeVar, "login01")
				path, err := write(&JobSpec{Name: "login", Command: "echo hi", Specs: spec, Metadata: map[string]string{}}, t.TempDir())
				if err != nil {
					t.Fatalf("CreateScriptWithSpec: %v", err)
				}
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(body), "export CNT_LOGIN_NODE=login01\n") {
					t.Errorf("script does not record the login node\n%s", body)
				}
			})
		}
	}
}

// Inside a job with no record, no node is known and the script exports none.
func TestScriptExportsNoLoginNodeWhenNoneIsKnown(t *testing.T) {
	clearJobEnvVars(t)
	t.Setenv("SLURM_JOB_ID", "1")
	unsetLoginNode(t)
	jobSpec := &JobSpec{Name: "none", Command: "echo hi", Specs: &ScriptSpecs{RemainingFlags: []string{}}, Metadata: map[string]string{}}
	path, err := newTestSlurmScheduler().CreateScriptWithSpec(jobSpec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), LoginNodeVar) {
		t.Errorf("a script exports a login node that is not known\n%s", body)
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

	// Only the CPUs and the memory of a rank are read. The rank variables are for display.
	t.Run("round trip", func(t *testing.T) {
		setExports(t, hybridSpec())
		res := resourcesFromExports()
		if res == nil {
			t.Fatal("expected non-nil")
		}
		if res.CpusPerTask != 2 || res.Nodes != 0 || res.Ntasks != 0 || res.TasksPerNode != 0 {
			t.Errorf("read %+v; want only 2 CPUs", res)
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

// nodeFile writes a PBS node file with one line per host given and returns its path.
func nodeFile(t *testing.T, hosts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nodes")
	if err := os.WriteFile(path, []byte(strings.Join(hosts, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The task count is CNT_NRANK first, then the scheduler's own variables. An MPI library's variables are not read.
func TestJobTaskCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want int
	}{
		{"no job", nil, 0},
		{"SLURM", map[string]string{"SLURM_JOB_ID": "1", "SLURM_NTASKS": "4"}, 4},
		{"PBS node file", map[string]string{"PBS_JOBID": "1.srv", "PBS_NODEFILE": nodeFile(t, "n1", "n1", "n2")}, 3},
		{"PBS node file with one rank", map[string]string{"PBS_JOBID": "1.srv", "PBS_NODEFILE": nodeFile(t, "n1"), "PBS_TASKNUM": "6"}, 1},
		{"PBS node file unreadable", map[string]string{"PBS_JOBID": "1.srv", "PBS_NODEFILE": "/missing/nodes"}, 0},
		{"LSF slots", map[string]string{"LSB_JOBID": "1", "LSB_DJOB_NUMPROC": "3"}, 3},
		{"one task", map[string]string{"SLURM_JOB_ID": "1", "SLURM_NTASKS": "1"}, 1},
		{"CNT_NRANK", map[string]string{"PBS_JOBID": "1.srv", "CNT_NRANK": "2", "PBS_NODEFILE": nodeFile(t, "n1")}, 2},
		{"CNT_NRANK wins", map[string]string{"SLURM_JOB_ID": "1", "CNT_NRANK": "9", "SLURM_NTASKS": "1"}, 9},
		{"MPI library is not read", map[string]string{"SLURM_JOB_ID": "1", "OMPI_COMM_WORLD_SIZE": "9"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearJobEnvVars(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := jobRankCount(); got != tc.want {
				t.Errorf("jobRankCount = %d, want %d", got, tc.want)
			}
			if got, want := InMultiRankJob(), tc.want > 1 && IsInsideJob(); got != want {
				t.Errorf("InMultiRankJob = %v, want %v", got, want)
			}
		})
	}
}

// A script run in place gets its CPUs and memory, and no rank variable.
func TestRunEnvVars(t *testing.T) {
	clearJobEnvVars(t)
	rs := &ResourceSpec{Nodes: 2, Ntasks: 8, TasksPerNode: 4, CpusPerTask: 2, MemPerNodeMB: 16384}
	got := map[string]string{}
	for _, kv := range RunEnvVars(rs, false) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]string{
		"NCPU": "2", "CNT_NCPU": "2", "MEM": "4096", "CNT_MEM": "4096", "MEM_GB": "4", "CNT_MEM_GB": "4",
		"OMP_NUM_THREADS": "2",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"NRANK", "NNODE", "NRANK_PER_NODE", "CNT_NRANK", "CNT_NNODE", "CNT_NRANK_PER_NODE"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %q is set; the rank variables belong to the job script header", k, v)
		}
	}
}

// OMP_NUM_THREADS is a default: a value that is set stays, unless the caller forces it.
func TestRunEnvVarsKeepsThreads(t *testing.T) {
	rs := &ResourceSpec{CpusPerTask: 4}
	threads := func(env []string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "OMP_NUM_THREADS="); ok {
				return v
			}
		}
		return ""
	}
	clearJobEnvVars(t)
	if got := threads(RunEnvVars(rs, false)); got != "4" {
		t.Errorf("unset: OMP_NUM_THREADS = %q, want 4", got)
	}
	t.Setenv("OMP_NUM_THREADS", "16")
	if got := threads(RunEnvVars(rs, false)); got != "" {
		t.Errorf("set: OMP_NUM_THREADS = %q, want it left alone", got)
	}
	if got := threads(RunEnvVars(rs, true)); got != "4" {
		t.Errorf("forced: OMP_NUM_THREADS = %q, want 4", got)
	}
}

// A job with more than one rank gets OMP_NUM_THREADS when it is unset, from the CPUs of one rank, and nothing else.
func TestMultiRankEnvVars(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"already set", map[string]string{"OMP_NUM_THREADS": "8", "CNT_NCPU": "2"}, nil},
		{"from the CondaTainer header", map[string]string{"CNT_NCPU": "2", "SLURM_CPUS_PER_TASK": "6"}, []string{"OMP_NUM_THREADS=2"}},
		{"from SLURM", map[string]string{"SLURM_CPUS_PER_TASK": "6"}, []string{"OMP_NUM_THREADS=6"}},
		{"nothing known", nil, []string{"OMP_NUM_THREADS=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearJobEnvVars(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got := MultiRankEnvVars()
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("MultiRankEnvVars = %v, want %v", got, tc.want)
			}
		})
	}
}

// A process with no active scheduler, such as a container in a job, reads the variables of the scheduler whose job it is in.
func TestCurrentJobResourcesWithoutAnActiveScheduler(t *testing.T) {
	ClearActiveScheduler()
	t.Cleanup(ClearActiveScheduler)
	clearJobEnvVars(t)
	t.Setenv("SLURM_JOB_ID", "1")
	t.Setenv("SLURM_CPUS_PER_TASK", "4")
	t.Setenv("SLURM_MEM_PER_NODE", "8192")
	res := CurrentJobResources()
	if res == nil || res.CpusPerTask != 4 || res.MemPerNodeMB != 8192 {
		t.Fatalf("got %+v, want 4 CPUs and 8192 MB from the SLURM variables", res)
	}

	clearJobEnvVars(t)
	if res := CurrentJobResources(); res != nil {
		t.Fatalf("got %+v outside a job", res)
	}
}
