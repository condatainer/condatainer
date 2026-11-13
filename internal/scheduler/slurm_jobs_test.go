package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// squeueLine builds a line the way squeue -O prints fields: each value padded to its width, then "|".
func squeueLine(fields []squeueField, values map[string]string) string {
	var b strings.Builder
	for _, f := range fields {
		v := values[f.name]
		if len(v) > f.width {
			v = v[:f.width]
		}
		b.WriteString(v)
		b.WriteString(strings.Repeat(" ", f.width-len(v)))
		b.WriteString("|")
	}
	return b.String()
}

func listLine(values map[string]string) string { return squeueLine(squeueListFields, values) }

func TestSqueueLineKeepsABarInAName(t *testing.T) {
	line := listLine(map[string]string{"ArrayJobID": "245070", "ArrayTaskID": "N/A", "Name": "a|b,c", "State": "RUNNING"})
	jobs, err := parseSqueueJobs(line+"\n", squeueListFields)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v, err = %v", jobs, err)
	}
	if jobs[0].Name != "a|b,c" || jobs[0].State != JobRunning {
		t.Errorf("job = %+v, want the name a|b,c and state running", jobs[0])
	}
}

func TestSqueueLineThatDoesNotSplitIsAnError(t *testing.T) {
	line := listLine(map[string]string{"ArrayJobID": "1"})
	if _, err := parseSqueueJobs(line[:40]+"\n", squeueListFields); err == nil {
		t.Error("a short line must be an error, not a job with missing columns")
	}
	if _, err := parseSqueueJobs("245070 step04 RUNNING\n", squeueListFields); err == nil {
		t.Error("a line in another layout must be an error")
	}
}

func TestSqueueFormatGivesEveryFieldAWidthAndABar(t *testing.T) {
	format := squeueFormat(append(append([]squeueField{}, squeueListFields...), squeueFullFields...))
	for _, part := range strings.Split(format, ",") {
		name, width, ok := strings.Cut(part, ":")
		if !ok || !strings.HasSuffix(width, "|") || name == "" {
			t.Errorf("field %q needs a width and a trailing bar", part)
		}
	}
}

func TestSlurmJobIDs(t *testing.T) {
	s := &SlurmScheduler{}
	for id, want := range map[string]bool{
		"245068": true, "245068_4": true, "": false, "abc": false, "245068_": false,
		"245068_[1-3]": false, "12+1": false, "step04": false,
	} {
		if got := s.IsJobID(id); got != want {
			t.Errorf("IsJobID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestSlurmJobStateMap(t *testing.T) {
	tests := map[string]JobState{
		"PENDING": JobPending, "CONFIGURING": JobPending, "STAGE_IN": JobPending, "REQUEUED": JobPending,
		"REQUEUE_HOLD": JobPending, "RESV_DEL_HOLD": JobPending,
		"RUNNING": JobRunning, "COMPLETING": JobRunning, "STAGE_OUT": JobRunning, "RESIZING": JobRunning,
		"SIGNALING": JobRunning, "SUSPENDED": JobRunning, "STOPPED": JobRunning,
		"COMPLETED": JobSuccess,
		"FAILED":    JobFailed, "TIMEOUT": JobFailed, "OUT_OF_MEMORY": JobFailed, "NODE_FAIL": JobFailed,
		"BOOT_FAIL": JobFailed, "PREEMPTED": JobFailed, "DEADLINE": JobFailed, "SPECIAL_EXIT": JobFailed,
		"CANCELLED": JobCancelled, "CANCELLED by 1000": JobCancelled, "REVOKED": JobCancelled,
		"REQUEUE_FED": JobUnknown, "": JobUnknown,
	}
	for state, want := range tests {
		if got, _ := slurmJobState(state); got != want {
			t.Errorf("state %q = %q, want %q", state, got, want)
		}
	}
	for state, want := range map[string]bool{"SUSPENDED": true, "STOPPED": true, "RUNNING": false} {
		if _, suspended := slurmJobState(state); suspended != want {
			t.Errorf("state %q suspended = %v, want %v", state, suspended, want)
		}
	}
}

func TestParseSlurmTRES(t *testing.T) {
	tests := []struct {
		tres  string
		cpus  int
		mem   int64
		nodes int
		gpus  []JobGpu
	}{
		{"cpu=4,mem=400M,node=1,billing=4000", 4, 400, 1, nil},
		{"cpu=8,mem=2G,node=2", 8, 2048, 2, nil},
		{"cpu=2,mem=766000M,node=1,gres/gpu=1,gres/gpu:a100=1", 2, 766000, 1, []JobGpu{{"a100", 1}}},
		{"cpu=1,mem=1G,node=1,gres/gpu=2", 1, 1024, 1, []JobGpu{{"gpu", 2}}},
		{"cpu=1,gres/gpu:nvidia_h100_80gb_hbm3_1g.10gb=1", 1, 0, 0, []JobGpu{{"nvidia_h100_80gb_hbm3_1g.10gb", 1}}},
		{"cpu=4,mem=1T,node=1,gres/gpu:a100=2,gres/gpu:h100=1", 4, 1024 * 1024, 1, []JobGpu{{"a100", 2}, {"h100", 1}}},
		{"", 0, 0, 0, nil},
	}
	for _, tt := range tests {
		cpus, mem, nodes, gpus := parseSlurmTRES(tt.tres)
		if cpus != tt.cpus || mem != tt.mem || nodes != tt.nodes || !reflect.DeepEqual(gpus, tt.gpus) {
			t.Errorf("%q = %d cpus, %d MB, %d nodes, %v; want %d, %d, %d, %v",
				tt.tres, cpus, mem, nodes, gpus, tt.cpus, tt.mem, tt.nodes, tt.gpus)
		}
	}
}

func TestParseSlurmDependencies(t *testing.T) {
	tests := []struct {
		field string
		cut   bool
		want  []JobDependency
	}{
		{"", false, nil},
		{"(null)", false, nil},
		{"afterok:26902114(unfulfilled)", false, []JobDependency{{Type: "afterok", JobID: "26902114"}}},
		{"afterok:1(unfulfilled),afterok:2(failed)", false, []JobDependency{
			{Type: "afterok", JobID: "1"}, {Type: "afterok", JobID: "2", Failed: true}}},
		{"afterany:1(unfulfilled)?afterok:2(unfulfilled)", false, []JobDependency{
			{Type: "afterany", JobID: "1"}, {Type: "afterok", JobID: "2"}}},
		{"aftercorr:7_*(unfulfilled)", false, []JobDependency{{Type: "aftercorr", JobID: "7_*"}}},
		{"singleton(unfulfilled)", false, []JobDependency{{Type: "singleton"}}},
		{"afterok:1(unfulfilled),afterok:22", true, []JobDependency{{Type: "afterok", JobID: "1"}}},
		{"afterok:5", false, []JobDependency{{Type: "afterok", JobID: "5", Satisfied: true}}},
	}
	for _, tt := range tests {
		got := parseSlurmDependencies(tt.field, tt.cut)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q cut=%v = %+v, want %+v", tt.field, tt.cut, got, tt.want)
		}
	}
}

func TestExpandSlurmNodeList(t *testing.T) {
	tests := map[string][]string{
		"":                            nil,
		"(null)":                      nil,
		"cpu-node-01":                 {"cpu-node-01"},
		"c[161-162]":                  {"c161", "c162"},
		"c[08-10,12],gpu3":            {"c08", "c09", "c10", "c12", "gpu3"},
		"rack[1-2]-n[1-2]":            {"rack1-n1", "rack1-n2", "rack2-n1", "rack2-n2"},
		"a.example.org,b.example.org": {"a.example.org", "b.example.org"},
		"c[x-y]":                      {"c[x-y]"},
	}
	for list, want := range tests {
		if got := expandSlurmNodeList(list); !slices.Equal(got, want) {
			t.Errorf("%q = %v, want %v", list, got, want)
		}
	}
}

func TestJobFromSqueueRunning(t *testing.T) {
	line := listLine(map[string]string{
		"ArrayJobID": "245075", "ArrayTaskID": "N/A", "Name": "fold_model", "State": "RUNNING", "Reason": "None",
		"Partition": "gpu", "NodeList": "gpu-node-02", "TimeUsed": "3:10:05", "TimeLimit": "12:00:00",
		"tres-alloc": "cpu=8,mem=64G,node=1,gres/gpu=1,gres/gpu:h100=1", "Dependency": "(null)",
	})
	jobs, err := parseSqueueJobs(line, squeueListFields)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v, err = %v", jobs, err)
	}
	want := JobInfo{
		ID: "245075", Name: "fold_model", State: JobRunning, Partitions: []string{"gpu"},
		NodeList: []string{"gpu-node-02"}, NodeCount: 1, Cpus: 8, MemMB: 64 * 1024,
		Gpus: []JobGpu{{"h100", 1}}, Elapsed: 3*time.Hour + 10*time.Minute + 5*time.Second, TimeLimit: 12 * time.Hour, Source: SourceQueue,
	}
	if !reflect.DeepEqual(jobs[0], want) {
		t.Errorf("job = %+v\nwant  %+v", jobs[0], want)
	}
}

func TestJobFromSqueuePendingKeepsItsReasonAndPartitions(t *testing.T) {
	line := listLine(map[string]string{
		"ArrayJobID": "245073", "ArrayTaskID": "N/A", "Name": "step05", "State": "PENDING", "Reason": "Dependency",
		"Partition": "small,large", "TimeLimit": "UNLIMITED", "tres-alloc": "cpu=4,mem=16G,node=1,billing=4",
		"Dependency": "afterok:245070(unfulfilled)",
	})
	jobs, _ := parseSqueueJobs(line, squeueListFields)
	j := jobs[0]
	if j.State != JobPending || j.Reason != "Dependency" || !slices.Equal(j.Partitions, []string{"small", "large"}) {
		t.Errorf("job = %+v", j)
	}
	if !j.TimeUnlimited || j.NodeList != nil || j.NodeCount != 1 {
		t.Errorf("unlimited = %v, node list = %v, count = %d", j.TimeUnlimited, j.NodeList, j.NodeCount)
	}
	if len(j.DependsOn) != 1 || j.DependsOn[0].JobID != "245070" {
		t.Errorf("depends on = %+v", j.DependsOn)
	}
}

func TestJobFromSqueueNotSetLimitIsNoLimitAtAll(t *testing.T) {
	line := listLine(map[string]string{"ArrayJobID": "1", "ArrayTaskID": "N/A", "State": "PENDING", "TimeLimit": "NOT_SET"})
	jobs, _ := parseSqueueJobs(line, squeueListFields)
	if jobs[0].TimeUnlimited || jobs[0].TimeLimit != 0 {
		t.Errorf("NOT_SET = %+v, want no limit figure", jobs[0])
	}
}

func TestJobFromSqueueCutNodeListUsesTheNodeCount(t *testing.T) {
	long := strings.Repeat("node-0001,", 60)
	line := listLine(map[string]string{
		"ArrayJobID": "9", "ArrayTaskID": "N/A", "State": "RUNNING", "NodeList": long, "tres-alloc": "cpu=600,mem=1G,node=60",
	})
	jobs, _ := parseSqueueJobs(line, squeueListFields)
	if jobs[0].NodeList != nil || jobs[0].NodeCount != 60 {
		t.Errorf("a node list cut at its width gave %v and %d nodes", jobs[0].NodeList, jobs[0].NodeCount)
	}
}

func TestJobFromSqueueArrayTask(t *testing.T) {
	line := listLine(map[string]string{"ArrayJobID": "248550", "ArrayTaskID": "4", "State": "RUNNING"})
	jobs, _ := parseSqueueJobs(line, squeueListFields)
	j := jobs[0]
	if j.ID != "248550_4" || j.ArrayID != "248550" || j.TaskID != "4" || !j.IsTask() {
		t.Errorf("task = %+v, want ID 248550_4 of array 248550", j)
	}
}

func TestFullFieldsAddTimesLogsAndLayout(t *testing.T) {
	fields := append(append([]squeueField{}, squeueListFields...), squeueFullFields...)
	line := squeueLine(fields, map[string]string{
		"ArrayJobID": "245080", "ArrayTaskID": "N/A", "State": "RUNNING",
		"SubmitTime": "2026-11-12T14:52:25", "StartTime": "2026-11-12T14:53:00", "EndTime": "N/A",
		"STDOUT": "/scratch/count_245080.out", "STDERR": "(null)", "NumTasks": "8", "cpus-per-task": "2",
	})
	jobs, err := parseSqueueJobs(line, fields)
	if err != nil {
		t.Fatal(err)
	}
	j := jobs[0]
	if j.SubmitTime.Format("2006-01-02 15:04:05") != "2026-11-12 14:52:25" || j.StartTime.IsZero() || !j.EndTime.IsZero() {
		t.Errorf("times = %v %v %v", j.SubmitTime, j.StartTime, j.EndTime)
	}
	if j.Stdout != "/scratch/count_245080.out" || j.Stderr != j.Stdout {
		t.Errorf("stdout = %q, stderr = %q: stderr repeats stdout when they are one file", j.Stdout, j.Stderr)
	}
	if j.Ranks != 8 || j.CpusPerRank != 2 {
		t.Errorf("layout = %d ranks x %d cpus", j.Ranks, j.CpusPerRank)
	}
}

func TestEstimatedTimesAreNotReported(t *testing.T) {
	fields := append(append([]squeueField{}, squeueListFields...), squeueFullFields...)
	times := map[string]string{"SubmitTime": "2026-11-12T10:00:00", "StartTime": "2026-11-12T11:00:00", "EndTime": "2026-11-12T23:00:00"}
	for _, tt := range []struct {
		state            string
		hasStart, hasEnd bool
	}{{"PENDING", false, false}, {"RUNNING", true, false}, {"COMPLETED", true, true}} {
		values := map[string]string{"ArrayJobID": "1", "ArrayTaskID": "N/A", "State": tt.state}
		for k, v := range times {
			values[k] = v
		}
		jobs, _ := parseSqueueJobs(squeueLine(fields, values), fields)
		if got := !jobs[0].StartTime.IsZero(); got != tt.hasStart {
			t.Errorf("%s: has start = %v, want %v", tt.state, got, tt.hasStart)
		}
		if got := !jobs[0].EndTime.IsZero(); got != tt.hasEnd {
			t.Errorf("%s: has end = %v, want %v", tt.state, got, tt.hasEnd)
		}
	}
}

func TestSingleRankJobHasNoLayout(t *testing.T) {
	fields := append(append([]squeueField{}, squeueListFields...), squeueFullFields...)
	for _, tasks := range []string{"1", "4294967294"} {
		line := squeueLine(fields, map[string]string{"ArrayJobID": "1", "ArrayTaskID": "N/A", "State": "PENDING", "NumTasks": tasks, "cpus-per-task": "4"})
		jobs, _ := parseSqueueJobs(line, fields)
		if jobs[0].Ranks != 0 || jobs[0].CpusPerRank != 0 {
			t.Errorf("NumTasks %s gave %d ranks x %d cpus, want none", tasks, jobs[0].Ranks, jobs[0].CpusPerRank)
		}
	}
}

func TestSqueueRunsWithoutUserDefaults(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "call")
	script := "#!/bin/sh\n{ echo \"$@\"; env | grep -E '^(SQUEUE_|SLURM_TIME_FORMAT|SLURM_CONF)' | sort; } > " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "squeue"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SQUEUE_STATES", "all")
	t.Setenv("SQUEUE_PARTITION", "elsewhere")
	t.Setenv("SLURM_TIME_FORMAT", "relative")
	t.Setenv("SLURM_CONF", "/etc/slurm.conf")

	s := &SlurmScheduler{sbatchBin: filepath.Join(dir, "sbatch")}
	if _, err := s.ListJobs(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	call := string(got)
	if strings.Contains(call, "SQUEUE_") || strings.Contains(call, "relative") {
		t.Errorf("squeue saw the user's defaults:\n%s", call)
	}
	for _, want := range []string{"SLURM_TIME_FORMAT=standard", "SLURM_CONF=/etc/slurm.conf", "--me -r -h -O ArrayJobID:20|"} {
		if !strings.Contains(call, want) {
			t.Errorf("call lacks %q:\n%s", want, call)
		}
	}
	if strings.Contains(call, "--expand-patterns") {
		t.Errorf("a table listing must not ask for the log paths:\n%s", call)
	}
}

func TestFullListingExpandsLogPatterns(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "call")
	if err := os.WriteFile(filepath.Join(dir, "squeue"), []byte("#!/bin/sh\necho \"$@\" > "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &SlurmScheduler{sbatchBin: filepath.Join(dir, "sbatch")}
	if _, err := s.ListJobs(context.Background(), ListOptions{Full: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	if !strings.Contains(string(got), "--expand-patterns") || !strings.Contains(string(got), "STDOUT:1024|") {
		t.Errorf("a full listing asks for the log paths, expanded: %s", got)
	}
}

func TestListJobsReportsAFailedSqueue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "squeue"), []byte("#!/bin/sh\necho 'slurm_load_jobs error: Unable to contact slurm controller' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &SlurmScheduler{sbatchBin: filepath.Join(dir, "sbatch")}
	jobs, err := s.ListJobs(context.Background(), ListOptions{})
	if err == nil || !strings.Contains(err.Error(), "Unable to contact slurm controller") || jobs != nil {
		t.Errorf("jobs = %v, err = %v: a failed call must be an error with squeue's message", jobs, err)
	}
}

func TestParseSlurmGresRequest(t *testing.T) {
	tests := map[string][]JobGpu{
		"":                              {},
		"N/A":                           {},
		"gres/gpu:quadro:2":             {{"quadro", 2}},
		"gres/gpu:2":                    {{"gpu", 2}},
		"gres/gpu":                      {{"gpu", 1}},
		"gres/gpu:a100:1,gres/gpu:t4:3": {{"a100", 1}, {"t4", 3}},
		"gres/gpu:nvidia_h100_80gb_hbm3_1g.10gb:1": {{"nvidia_h100_80gb_hbm3_1g.10gb", 1}},
		"gres/shard:4,gres/gpu:l40:1":              {{"l40", 1}},
		"gres/gpu:l40:x":                           {},
	}
	for in, want := range tests {
		got := parseSlurmGresRequest(in)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
}

func TestJobGpusFromTheRequestWhenTresAllocHasNone(t *testing.T) {
	line := func(values map[string]string) JobInfo {
		values["ArrayJobID"], values["ArrayTaskID"] = "1", "N/A"
		jobs, _ := parseSqueueJobs(listLine(values), squeueListFields)
		return jobs[0]
	}
	perNode := line(map[string]string{"State": "PENDING", "tres-alloc": "cpu=1,mem=100M,node=2", "tres-per-node": "gres/gpu:quadro:2", "tres-per-job": "N/A"})
	if !reflect.DeepEqual(perNode.Gpus, []JobGpu{{"quadro", 4}}) {
		t.Errorf("2 per node on 2 nodes = %v, want 4", perNode.Gpus)
	}
	perJob := line(map[string]string{"State": "PENDING", "tres-alloc": "cpu=1,node=1", "tres-per-node": "N/A", "tres-per-job": "gres/gpu:1"})
	if !reflect.DeepEqual(perJob.Gpus, []JobGpu{{"gpu", 1}}) {
		t.Errorf("per job = %v", perJob.Gpus)
	}
	counted := line(map[string]string{"State": "RUNNING", "tres-alloc": "cpu=1,node=1,gres/gpu:a100=1", "tres-per-node": "gres/gpu:t4:8"})
	if !reflect.DeepEqual(counted.Gpus, []JobGpu{{"a100", 1}}) {
		t.Errorf("the allocation wins over the request: %v", counted.Gpus)
	}
	if none := line(map[string]string{"State": "PENDING", "tres-alloc": "cpu=1,node=1", "tres-per-node": "N/A", "tres-per-job": "N/A"}); len(none.Gpus) != 0 {
		t.Errorf("no GPU asked for: %v", none.Gpus)
	}
}

func TestRanksPerNodeIsSetOnlyWhenTheUserFixedIt(t *testing.T) {
	line := func(ntasks, perNode string) JobInfo {
		jobs, _ := parseSqueueJobs(listLine(map[string]string{
			"ArrayJobID": "1", "ArrayTaskID": "N/A", "State": "PENDING", "tres-alloc": "cpu=8,mem=200M,node=2",
			"NumTasks": ntasks, "NTPerNode": perNode, "cpus-per-task": "2",
		}), squeueListFields)
		return jobs[0]
	}
	if j := line("4", "0"); j.Ranks != 4 || j.CpusPerRank != 2 || j.RanksPerNode != 0 {
		t.Errorf("tasks only = %d ranks x %d cpus, %d per node; want 4 x 2 and no split", j.Ranks, j.CpusPerRank, j.RanksPerNode)
	}
	if j := line("4", "2"); j.Ranks != 4 || j.RanksPerNode != 2 {
		t.Errorf("tasks per node = %d ranks, %d per node; want 4 and 2", j.Ranks, j.RanksPerNode)
	}
	if j := line("1", "0"); j.Ranks != 0 || j.RanksPerNode != 0 {
		t.Errorf("one rank has no layout: %+v", j)
	}
}
