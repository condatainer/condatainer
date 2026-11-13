package scheduler

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
)

func TestFormatMem(t *testing.T) {
	tests := map[int64]string{
		512:      "512M",
		1000:     "1000M",
		8192:     "8192M",
		10239:    "10239M",
		10240:    "10G",
		766000:   "748G",
		507000:   "495G",
		2048000:  "2000G",
		3045514:  "2974G",
		10485759: "10240G",
		10485760: "10T",
		12582912: "12T",
		11010048: "10.5T",
	}
	for mb, want := range tests {
		if got := formatMem(mb); got != want {
			t.Errorf("formatMem(%d) = %q, want %q", mb, got, want)
		}
	}
}

func TestFormatHM(t *testing.T) {
	tests := map[time.Duration]string{
		8 * time.Hour:              "08:00",
		72 * time.Hour:             "72:00",
		time.Hour + 5*time.Minute:  "01:05",
		7 * 24 * time.Hour:         "168:00",
		0:                          "00:00",
		59*time.Minute + time.Hour: "01:59",
	}
	for d, want := range tests {
		if got := formatHM(d); got != want {
			t.Errorf("formatHM(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPartitionTable(t *testing.T) {
	limits := []ResourceLimits{
		{Partition: "cpubase_interac", State: "up", MaxTime: 8 * time.Hour, Nodes: 702, MaxCpusPerNode: 192, MaxMemMBPerNode: 766000,
			NodeTypes: []NodeType{{Nodes: 702, Cpus: 192, MemMB: 766000}}},
		{Partition: "cpubackfill", State: "up", Default: true, MaxTime: 24 * time.Hour, Nodes: 709,
			NodeTypes: []NodeType{{Nodes: 709, Cpus: 192, MemMB: 766000, MemLowerBound: true}}},
		{Partition: "gpubase_bynode_b4", State: "up", MaxTime: 72 * time.Hour,
			NodeTypes: []NodeType{
				{Nodes: 6, Cpus: 112, MemMB: 2048000, Gpus: []NodeGpu{{Type: "h100", PerNode: 8}}},
				{Nodes: 5, Cpus: 112, MemMB: 2048000, Gpus: []NodeGpu{
					{Type: "nvidia_h100_80gb_hbm3_3g.40gb", PerNode: 8}, {Type: "nvidia_h100_80gb_hbm3_1g.10gb", PerNode: 16}}},
				{Nodes: 4, Cpus: 96, MemMB: 507000, Gpus: []NodeGpu{{Type: "mi300a", PerNode: 4}}},
			}},
		{Partition: "retired", State: "down", MaxTime: time.Hour, MaxNodes: 4, Nodes: 3, NodeTypes: []NodeType{{Nodes: 3, Cpus: 8, MemMB: 1000}}},
	}
	limits[2].Nodes = 15

	var out bytes.Buffer
	WritePartitionTable(&out, limits, nil)

	want := `PARTITION          AVAIL  NODES  CPUS    MEM  MAXTIME  MAXNODES  GPUS
cpubase_interac    up       702   192   748G    08:00         -  -
cpubackfill*       up       709   192  748G+    24:00         -  -
gpubase_bynode_b4  up         6   112  2000G    72:00         -  h100:8
                              5   112  2000G                     nvidia_h100_80gb_hbm3_3g.40gb:8
                                                                 nvidia_h100_80gb_hbm3_1g.10gb:16
                              4    96   495G                     mi300a:4
retired            down       3     8  1000M    01:00         4  -
`
	if got := stripColor(out.String()); got != want {
		t.Errorf("table mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Node types whose memory is within 10% share a row, at the smallest size with a +.
func TestPartitionTableMergesMemoryClasses(t *testing.T) {
	gpu := []NodeGpu{{Type: "t4", PerNode: 2}}
	limits := []ResourceLimits{
		{Partition: "a", State: "up", NodeTypes: []NodeType{
			{Nodes: 1, Cpus: 64, MemMB: 524072},
			{Nodes: 1, Cpus: 64, MemMB: 523547},
			{Nodes: 203, Cpus: 192, MemMB: 766000},
			{Nodes: 10, Cpus: 192, MemMB: 6144000},
			{Nodes: 2, Cpus: 64, MemMB: 524072, Gpus: gpu},
			{Nodes: 2, Cpus: 64, MemMB: 524072},
		}},
	}
	var out bytes.Buffer
	WritePartitionTable(&out, limits, nil)
	want := `PARTITION  AVAIL  NODES  CPUS    MEM  MAXTIME  GPUS
a          up         4    64  511G+        -  -
                    203   192   748G           -
                     10   192  6000G           -
                      2    64   512G           t4:2
`
	if got := stripColor(out.String()); got != want {
		t.Errorf("table mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A table whose partitions have no node cap leaves the MAXNODES column out.
// An unlimited time is "inf", and a time the scheduler does not report is "-".
func TestPartitionTableWithoutNodeCap(t *testing.T) {
	limits := []ResourceLimits{
		{Partition: "a", State: "up", MaxTime: time.Hour, NodeTypes: []NodeType{{Nodes: 2, Cpus: 8, MemMB: 16384}}},
		{Partition: "b", State: "up", TimeUnlimited: true, NodeTypes: []NodeType{{Nodes: 2, Cpus: 8, MemMB: 16384}}},
		{Partition: "c", State: "up", NodeTypes: []NodeType{{Nodes: 2, Cpus: 8, MemMB: 16384}}},
	}
	var out bytes.Buffer
	WritePartitionTable(&out, limits, nil)
	want := `PARTITION  AVAIL  NODES  CPUS  MEM  MAXTIME  GPUS
a          up         2     8  16G    01:00  -
b          up         2     8  16G      inf  -
c          up         2     8  16G        -  -
`
	if got := stripColor(out.String()); got != want {
		t.Errorf("table mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

var colorRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripColor(s string) string { return colorRe.ReplaceAllString(s, "") }

func TestPartitionRowsWithoutNodeTypes(t *testing.T) {
	l := ResourceLimits{Partition: "q1", Nodes: 0, MaxCpusPerNode: 32, MaxMemMBPerNode: 65536}
	rows := partitionRows(l, []GpuInfo{{Type: "v100", Total: 4}})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(rows), rows)
	}
	want := []string{"q1", "-", "-", "32", "64G", "-", "-", "v100"}
	for i, w := range want {
		if rows[0][i] != w {
			t.Errorf("column %s = %q, want %q", partitionColumns[i], rows[0][i], w)
		}
	}
}

func jobTableText(jobs []JobInfo, width int) string {
	var b bytes.Buffer
	WriteJobTable(&b, jobs, width)
	return b.String()
}

func sampleJobs() []JobInfo {
	return []JobInfo{
		{ID: "245070", Name: "step04_star", State: JobRunning, Cpus: 16, MemMB: 72 * 1024, Elapsed: 72 * time.Minute, TimeLimit: 24 * time.Hour,
			NodeList: []string{"cpu-node-01"}, NodeCount: 1},
		{ID: "245071", Name: "step04_star", State: JobPending, Cpus: 16, MemMB: 72 * 1024, TimeLimit: 24 * time.Hour, Reason: "Dependency",
			DependsOn: []JobDependency{{Type: "afterok", JobID: "245069", Failed: true}}},
		{ID: "245073", Name: "step05_count", State: JobPending, Cpus: 4, MemMB: 16 * 1024, TimeLimit: 4 * time.Hour, Reason: "Dependency",
			DependsOn: []JobDependency{{Type: "afterok", JobID: "245070"}}},
		{ID: "245080", ArrayID: "245080", Name: "step05_count", State: JobRunning, Cpus: 4, MemMB: 16 * 1024, TimeLimit: 4 * time.Hour,
			Tasks: &ArrayTasks{Running: []string{"245080_1", "245080_2", "245080_3"}, Pending: []string{"245080_4", "245080_5"}}},
	}
}

func TestJobTable(t *testing.T) {
	got := jobTableText(sampleJobs(), 100)
	want := `JOBID   NAME          STATE    CPUS  MEM  TIME         NODE         NOTE
245070  step04_star   running    16  72G  01:12/24:00  cpu-node-01
245071  step04_star   pending    16  72G  24:00        -            dep failed 245069
245073  step05_count  pending     4  16G  04:00        -            after 245070
245080  step05_count  running     4  16G  04:00        -            3 running, 2 pending
`
	if got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

func TestJobTableShowsGPUsOnlyWhenAJobHasOne(t *testing.T) {
	jobs := sampleJobs()
	if strings.Contains(jobTableText(jobs, 100), "GPUS") {
		t.Error("no job has a GPU, so the column must be hidden")
	}
	jobs[0].Gpus = []JobGpu{{Type: "h100", Count: 1}}
	got := jobTableText(jobs, 100)
	if !strings.Contains(got, "GPUS") || !strings.Contains(got, "h100:1") {
		t.Errorf("a GPU job must show the column:\n%s", got)
	}
}

func TestJobTableWithNoJobsIsTheHeader(t *testing.T) {
	got := jobTableText(nil, 100)
	if strings.Count(got, "\n") != 1 || !strings.HasPrefix(got, "JOBID") {
		t.Errorf("got %q, want the header only", got)
	}
}

func TestJobTableCutsNameAndNoteToFit(t *testing.T) {
	jobs := []JobInfo{{ID: "1", Name: strings.Repeat("n", 60), State: JobPending, Reason: strings.Repeat("r", 40), Cpus: 1, MemMB: 1024, TimeLimit: time.Hour}}
	for _, line := range strings.Split(strings.TrimRight(jobTableText(jobs, 80), "\n"), "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("line is %d wide, want at most 80: %q", n, line)
		}
	}
	got := jobTableText(jobs, 80)
	if !strings.Contains(got, "…") {
		t.Errorf("a cut value ends in …:\n%s", got)
	}
}

func TestJobTableKeepsAShortNoteWhole(t *testing.T) {
	jobs := []JobInfo{{ID: "1", Name: strings.Repeat("n", 90), State: JobPending, Reason: "Priority", Cpus: 1, MemMB: 1024, TimeLimit: time.Hour}}
	got := jobTableText(jobs, 80)
	if !strings.Contains(got, "Priority") {
		t.Errorf("the long name takes the cut, not the short note:\n%s", got)
	}
}

func TestJobNode(t *testing.T) {
	tests := []struct {
		name string
		job  JobInfo
		want string
	}{
		{"one node, domain cut", JobInfo{State: JobRunning, NodeList: []string{"node03.example.org"}}, "node03"},
		{"list that fits", JobInfo{State: JobRunning, NodeList: []string{"c161", "c162"}}, "c161,c162"},
		{"list too long", JobInfo{State: JobRunning, NodeList: []string{"node-aaaaaa", "node-bbbbbb", "node-cccccc"}}, "3 nodes"},
		{"one long name", JobInfo{State: JobRunning, NodeList: []string{strings.Repeat("x", 40)}}, strings.Repeat("x", 25) + "…"},
		{"list cut by the scheduler", JobInfo{State: JobRunning, NodeCount: 60}, "60 nodes"},
		{"pending", JobInfo{State: JobPending}, "-"},
		{"an ended job keeps its node", JobInfo{State: JobSuccess, NodeList: []string{"c1.example.org"}}, "c1"},
		{"never had a node", JobInfo{State: JobCancelled}, "-"},
		{"array", JobInfo{State: JobRunning, Tasks: &ArrayTasks{}, NodeList: []string{"c1"}}, "-"},
	}
	for _, tt := range tests {
		if got := jobNode(tt.job); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestJobTime(t *testing.T) {
	tests := []struct {
		name string
		job  JobInfo
		want string
	}{
		{"pending shows the limit", JobInfo{State: JobPending, TimeLimit: 24 * time.Hour}, "24:00"},
		{"running shows elapsed over limit", JobInfo{State: JobRunning, Elapsed: 3*time.Hour + 10*time.Minute + 59*time.Second, TimeLimit: 12 * time.Hour}, "03:10/12:00"},
		{"hours do not wrap", JobInfo{State: JobPending, TimeLimit: 72 * time.Hour}, "72:00"},
		{"unlimited", JobInfo{State: JobRunning, Elapsed: time.Hour, TimeUnlimited: true}, "01:00/inf"},
		{"not set", JobInfo{State: JobPending}, "-"},
		{"an ended job shows its elapsed time", JobInfo{State: JobSuccess, Elapsed: 48*time.Minute + 6*time.Second, TimeLimit: time.Hour}, "00:48"},
		{"an ended job that never ran", JobInfo{State: JobCancelled, TimeLimit: time.Hour}, "-"},
		{"an array shows its limit only", JobInfo{State: JobRunning, Elapsed: time.Hour, TimeLimit: 4 * time.Hour, Tasks: &ArrayTasks{}}, "04:00"},
	}
	for _, tt := range tests {
		if got := jobTime(tt.job); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestJobNote(t *testing.T) {
	tests := []struct {
		name string
		job  JobInfo
		want string
	}{
		{"reason", JobInfo{State: JobPending, Reason: "QOSMaxJobsPerUserLimit"}, "QOSMaxJobsPerUserLimit"},
		{"running has no note", JobInfo{State: JobRunning, Reason: "None"}, ""},
		{"suspended", JobInfo{State: JobRunning, Suspended: true}, "suspended"},
		{"afterok", JobInfo{State: JobPending, DependsOn: []JobDependency{{Type: "afterok", JobID: "5"}}}, "after 5"},
		{"afternotok", JobInfo{State: JobPending, DependsOn: []JobDependency{{Type: "afternotok", JobID: "5"}}}, "afternotok 5"},
		{"afterany", JobInfo{State: JobPending, DependsOn: []JobDependency{{Type: "afterany", JobID: "5"}}}, "afterany 5"},
		{"singleton", JobInfo{State: JobPending, DependsOn: []JobDependency{{Type: "singleton"}}}, "singleton"},
		{"two open parents", JobInfo{State: JobPending, DependsOn: []JobDependency{{Type: "afterok", JobID: "5"}, {Type: "afterok", JobID: "6"}}}, "after 5 +1"},
		{"a failed parent comes first", JobInfo{State: JobPending, DependsOn: []JobDependency{{Type: "afterok", JobID: "5"}, {Type: "afterok", JobID: "6", Failed: true}}}, "dep failed 6"},
		{"met dependency leaves the reason", JobInfo{State: JobPending, Reason: "Priority", DependsOn: []JobDependency{{Type: "afterok", JobID: "5", Satisfied: true}}}, "Priority"},
		{"array with only running tasks", JobInfo{State: JobRunning, Tasks: &ArrayTasks{Running: []string{"1_1"}}}, "1 running"},
		{"failed with an exit code", JobInfo{State: JobFailed, Failure: FailureNonzeroExit, ExitCode: intp(3)}, "exit 3"},
		{"failed by the scheduler", JobInfo{State: JobFailed, Failure: FailureOutOfMemory}, "out_of_memory"},
		{"cancelled child of a failed parent", JobInfo{State: JobCancelled, DependsOn: []JobDependency{{Type: "afterok", JobID: "5", Failed: true}}}, "dep failed 5"},
		{"success", JobInfo{State: JobSuccess, Reason: "None"}, ""},
		{"ended array", JobInfo{State: JobFailed, Tasks: &ArrayTasks{Success: []string{"a", "b"}, Failed: []string{"c"}}}, "2 success, 1 failed"},
		{"fixed ranks per node", JobInfo{State: JobRunning, NodeCount: 2, Ranks: 8, RanksPerNode: 4, CpusPerRank: 2}, "2n x 4r x 2c"},
		{"ranks the scheduler placed", JobInfo{State: JobRunning, NodeCount: 2, Ranks: 8, CpusPerRank: 2}, "8r x 2c"},
		{"layout comes before the reason", JobInfo{State: JobPending, Reason: "Resources", NodeCount: 2, Ranks: 8, RanksPerNode: 4, CpusPerRank: 2}, "2n x 4r x 2c, Resources"},
		{"a job of one rank has no layout", JobInfo{State: JobRunning, NodeCount: 2, Ranks: 0}, ""},
		{"an array has no layout", JobInfo{State: JobRunning, Ranks: 4, Tasks: &ArrayTasks{Running: []string{"a"}}}, "1 running"},
		{"array with only pending tasks", JobInfo{State: JobPending, Tasks: &ArrayTasks{Pending: []string{"1_1", "1_2"}}}, "2 pending"},
	}
	for _, tt := range tests {
		if got := jobNote(tt.job); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestJobTableColoursOnlyAFailedState(t *testing.T) {
	was := color.NoColor
	t.Cleanup(func() { color.NoColor = was })
	jobs := []JobInfo{{ID: "1", State: JobFailed}, {ID: "2", State: JobCancelled}, {ID: "3", State: JobSuccess}}

	color.NoColor = false
	lines := strings.Split(strings.TrimRight(jobTableText(jobs, 100), "\n"), "\n")
	for i, want := range []bool{true, false, false} {
		if got := strings.Contains(lines[i+1], "\x1b["); got != want {
			t.Errorf("job %d coloured = %v, want %v", i+1, got, want)
		}
	}
	color.NoColor = true
	if strings.Contains(jobTableText(jobs, 100), "\x1b[") {
		t.Error("no colour is written when colour is off")
	}
}

func intp(n int) *int { return &n }

func TestJobTableCutsNameAndNoteNoLowerThanTheMinimum(t *testing.T) {
	jobs := []JobInfo{{ID: "1", Name: strings.Repeat("n", 40), State: JobPending, Reason: strings.Repeat("r", 40), Cpus: 1, MemMB: 1024, TimeLimit: time.Hour}}
	got := jobTableText(jobs, 10)
	if !strings.Contains(got, strings.Repeat("n", minFlexWidth-1)+"…") || !strings.Contains(got, strings.Repeat("r", minFlexWidth-1)+"…") {
		t.Errorf("a narrow terminal keeps %d columns for the name and the note:\n%s", minFlexWidth, got)
	}
}
