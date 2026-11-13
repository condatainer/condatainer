package scheduler

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func detailText(j JobInfo) string {
	var b bytes.Buffer
	WriteJobDetail(&b, j)
	return b.String()
}

func at(h, m, s int) time.Time { return time.Date(2026, 11, 12, h, m, s, 0, time.Local) }

func TestJobDetailOfASuccessfulJob(t *testing.T) {
	code, cpu := 0, 2*time.Hour+57*time.Minute
	j := JobInfo{
		ID: "248534", Name: "realign", State: JobSuccess, Partitions: []string{"large"}, ExitCode: &code,
		Cpus: 4, MemMB: 40 * 1024, NodeList: []string{"node02.example.org"}, NodeCount: 1,
		TimeLimit: time.Hour, Elapsed: 48*time.Minute + 6*time.Second, CpuTime: &cpu, PeakMemMB: 11617,
		SubmitTime: at(14, 52, 25), StartTime: at(14, 52, 25), EndTime: at(15, 40, 31),
		Stdout: "/scratch/realign.log", Stderr: "/scratch/realign.log",
	}
	want := `248534  success  realign
  partitions   large
  exit code    0
  resources    4 cpus, 40 GB
  node list    node02.example.org
  time limit   01:00:00
  elapsed      00:48:06
  usage        cpu time 02:57:00, peak mem 11 GB
  submit time  2026-11-12 14:52:25
  start time   2026-11-12 14:52:25
  end time     2026-11-12 15:40:31
  queued       00:00:00
  stdout       /scratch/realign.log
`
	if got := detailText(j); got != want {
		t.Errorf("detail:\n%s\nwant:\n%s", got, want)
	}
}

func TestJobDetailOfAFailedJobHasItsReasonAndNoExitCode(t *testing.T) {
	cpu := 21*time.Hour + 12*time.Minute + 8*time.Second
	j := JobInfo{
		ID: "248600", Name: "align_sample", State: JobFailed, Failure: FailureOutOfMemory, Partitions: []string{"large"},
		Cpus: 8, MemMB: 80 * 1024, TimeLimit: 12 * time.Hour, Elapsed: 3*time.Hour + 2*time.Minute + 34*time.Second,
		CpuTime: &cpu, PeakMemMB: 80700, SubmitTime: at(10, 27, 25), StartTime: at(10, 30, 2), EndTime: at(13, 32, 36),
		Stdout: "/scratch/align_248600.out",
	}
	got := detailText(j)
	for _, want := range []string{"  failure      out_of_memory\n", "  usage        cpu time 21:12:08, peak mem 79 GB\n", "  queued       00:02:37\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "exit code") {
		t.Errorf("a job the scheduler ended has no exit code:\n%s", got)
	}
}

func TestJobDetailOfAPendingJob(t *testing.T) {
	j := JobInfo{ID: "245073", Name: "step05_count", State: JobPending, Partitions: []string{"large", "general"}, Reason: "Dependency",
		DependsOn: []JobDependency{{Type: "afterok", JobID: "245070"}}, Cpus: 4, MemMB: 16 * 1024, TimeLimit: 4 * time.Hour}
	got := detailText(j)
	for _, want := range []string{"  partitions  large, general\n", "  reason      Dependency\n", "  depends on  afterok:245070 unfulfilled\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail lacks %q:\n%s", want, got)
		}
	}
	for _, none := range []string{"elapsed", "usage", "start time", "node list", "queued"} {
		if strings.Contains(got, none) {
			t.Errorf("a waiting job has no %q:\n%s", none, got)
		}
	}
}

func TestJobDetailOfAnArray(t *testing.T) {
	j := JobInfo{
		ID: "248550", ArrayID: "248550", Name: "step05_count", State: JobFailed, Partitions: []string{"large"},
		Cpus: 4, MemMB: 16 * 1024, TimeLimit: 4 * time.Hour,
		SubmitTime: at(14, 58, 11), StartTime: at(15, 2, 11), EndTime: at(16, 41, 3), Stdout: "/scratch/count_%A_%a.out",
		Tasks: &ArrayTasks{Success: []string{"248550_1", "248550_3", "248550_4", "248550_5", "248550_6", "248550_7", "248550_8", "248550_9"}, Failed: []string{"248550_2"}},
	}
	want := `248550  failed  step05_count
  partitions   large
  resources    4 cpus, 16 GB
  time limit   04:00:00
  tasks        9: 8 success, 1 failed
  failed       248550_2
  submit time  2026-11-12 14:58:11
  start time   2026-11-12 15:02:11
  end time     2026-11-12 16:41:03
  queued       00:04:00
  stdout       /scratch/count_%A_%a.out
`
	if got := detailText(j); got != want {
		t.Errorf("detail:\n%s\nwant:\n%s", got, want)
	}
}

func TestJobDetailListsAtMostTenTasks(t *testing.T) {
	var failed []string
	for i := 1; i <= 14; i++ {
		failed = append(failed, "9_"+string(rune('a'+i)))
	}
	got := detailText(JobInfo{ID: "9", State: JobFailed, Tasks: &ArrayTasks{Failed: failed}})
	if !strings.Contains(got, "(+4 more)") || strings.Contains(got, failed[10]) {
		t.Errorf("detail:\n%s", got)
	}
}

func TestJobDetailLayout(t *testing.T) {
	tests := []struct {
		name string
		job  JobInfo
		want string
	}{
		{"fixed ranks per node", JobInfo{Cpus: 16, MemMB: 32 * 1024, NodeCount: 2, Ranks: 8, RanksPerNode: 4, CpusPerRank: 2},
			"16 cpus, 32 GB (2 nodes x 4 ranks x 2 cpus)"},
		{"placed by the scheduler", JobInfo{Cpus: 16, MemMB: 32 * 1024, NodeCount: 2, Ranks: 8, CpusPerRank: 2},
			"16 cpus, 32 GB (8 ranks x 2 cpus on 2 nodes)"},
		{"ranks on one node", JobInfo{Cpus: 8, NodeCount: 1, Ranks: 4, CpusPerRank: 2}, "8 cpus (4 ranks x 2 cpus)"},
		{"fixed per node but the counts disagree", JobInfo{Cpus: 8, NodeCount: 3, Ranks: 4, RanksPerNode: 2, CpusPerRank: 2},
			"8 cpus (4 ranks x 2 cpus on 3 nodes)"},
		{"one rank on two nodes", JobInfo{Cpus: 4, NodeCount: 2, Ranks: 0}, "4 cpus (2 nodes)"},
		{"one rank on one node", JobInfo{Cpus: 4, NodeCount: 1}, "4 cpus"},
	}
	for _, tt := range tests {
		if got := resourcesText(tt.job); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := resourcesText(JobInfo{Cpus: 1, MemMB: 400, Gpus: []JobGpu{{Type: "h100", Count: 1}}}); got != "1 cpu, 400 MB, 1 h100" {
		t.Errorf("resources = %q", got)
	}
}

func TestJobDetailStderrOnlyWhenItDiffers(t *testing.T) {
	same := detailText(JobInfo{ID: "1", Stdout: "/a.out", Stderr: "/a.out"})
	other := detailText(JobInfo{ID: "1", Stdout: "/a.out", Stderr: "/a.err"})
	if strings.Contains(same, "stderr") || !strings.Contains(other, "stderr") {
		t.Errorf("same:\n%s\nother:\n%s", same, other)
	}
}

func TestJobDetailOfAnIDNoSchedulerKnows(t *testing.T) {
	if got := detailText(JobInfo{ID: "999", State: JobNotFound}); got != "999  notfound\n" {
		t.Errorf("got %q", got)
	}
}
