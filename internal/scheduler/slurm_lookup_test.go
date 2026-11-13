package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeSlurm is a directory with a squeue and a sacct that print the given output and exit with the given status.
// calls reads the lines the two commands logged, each "squeue <args>" or "sacct <args>".
type fakeSlurm struct {
	*SlurmScheduler
	dir string
}

func newFakeSlurm(t *testing.T, squeueOut string, squeueRC int, sacctOut string, sacctRC int) *fakeSlurm {
	t.Helper()
	dir := t.TempDir()
	write := func(name, out string, rc int) {
		if err := os.WriteFile(filepath.Join(dir, name+".out"), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("#!/bin/sh\necho \"%s $@\" >> %s/calls\nenv | grep -E '^(SACCT_|SQUEUE_|SLURM_TIME_FORMAT)' | sort >> %s/env.%s\ncat %s/%s.out\nexit %d\n",
			name, dir, dir, name, dir, name, rc)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("squeue", squeueOut, squeueRC)
	write("sacct", sacctOut, sacctRC)
	return &fakeSlurm{SlurmScheduler: &SlurmScheduler{sbatchBin: filepath.Join(dir, "sbatch")}, dir: dir}
}

func (f *fakeSlurm) calls() []string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f *fakeSlurm) count(cmd string) int {
	n := 0
	for _, c := range f.calls() {
		if strings.HasPrefix(c, cmd+" ") {
			n++
		}
	}
	return n
}

func lookupLine(values map[string]string) string {
	return squeueLine(squeueLookupFields, values) + "\n"
}

// sacctRow builds a sacct -P row of sacctFields.
func sacctRow(values map[string]string) string {
	cols := make([]string, len(sacctFields))
	for i, f := range sacctFields {
		cols[i] = values[f]
	}
	return strings.Join(cols, "|") + "\n"
}

func lookup(t *testing.T, f *fakeSlurm, ids ...string) []JobInfo {
	t.Helper()
	jobs, err := f.LookupJobs(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func TestLookupOfAWaitingOrRunningJobNeedsNoSacct(t *testing.T) {
	out := lookupLine(map[string]string{"ArrayJobID": "5", "ArrayTaskID": "N/A", "State": "RUNNING", "Name": "run", "TimeUsed": "10:00", "TimeLimit": "1:00:00"}) +
		lookupLine(map[string]string{"ArrayJobID": "6", "ArrayTaskID": "N/A", "State": "PENDING", "Reason": "Priority"}) +
		lookupLine(map[string]string{"ArrayJobID": "7", "ArrayTaskID": "3", "State": "RUNNING"})
	f := newFakeSlurm(t, out, 0, "", 0)
	jobs := lookup(t, f, "5", "6", "7_3")
	if len(jobs) != 3 || jobs[0].State != JobRunning || jobs[1].Reason != "Priority" || jobs[2].ID != "7_3" {
		t.Errorf("jobs = %+v", jobs)
	}
	if f.count("squeue") != 1 || f.count("sacct") != 0 {
		t.Errorf("calls = %v, want one squeue and no sacct", f.calls())
	}
	if args := f.calls()[0]; !strings.Contains(args, "--me -r -h -t all --expand-patterns -j 5,6,7_3 ") {
		t.Errorf("squeue call = %s", args)
	}
}

func TestLookupAsksSacctOnceForEveryIDItNeeds(t *testing.T) {
	out := lookupLine(map[string]string{"ArrayJobID": "5", "ArrayTaskID": "N/A", "State": "RUNNING"}) +
		lookupLine(map[string]string{"ArrayJobID": "6", "ArrayTaskID": "N/A", "State": "COMPLETED", "exit_code": "0:0"})
	f := newFakeSlurm(t, out, 0, "", 0)
	lookup(t, f, "5", "6", "7", "8")
	if f.count("sacct") != 1 {
		t.Fatalf("calls = %v, want one sacct", f.calls())
	}
	if sacct := f.calls()[1]; !strings.Contains(sacct, "-j 6,7,8 ") || !strings.Contains(sacct, "--array") {
		t.Errorf("sacct asks for %s, want the ended job and the two unknown ones, with --array", sacct)
	}
}

func TestLookupFinishedJobGetsItsUsageFromSacct(t *testing.T) {
	out := lookupLine(map[string]string{"ArrayJobID": "6", "ArrayTaskID": "N/A", "State": "COMPLETED", "exit_code": "0:0", "Name": "realign",
		"STDOUT": "/s/realign_6.log", "tres-alloc": "cpu=4,mem=40G,node=1"})
	sacct := sacctRow(map[string]string{"JobID": "6", "State": "COMPLETED", "ExitCode": "0:0", "Start": "2026-11-12T14:52:25", "TotalCPU": "02:57:00", "JobName": "realign"}) +
		sacctRow(map[string]string{"JobID": "6.batch", "State": "COMPLETED", "MaxRSS": "11896032K"}) +
		sacctRow(map[string]string{"JobID": "6.0", "State": "COMPLETED", "MaxRSS": "15748K"}) +
		sacctRow(map[string]string{"JobID": "6.extern", "State": "COMPLETED"})
	j := lookup(t, newFakeSlurm(t, out, 0, sacct, 0), "6")[0]
	if j.State != JobSuccess || j.Source != SourceHistory || j.Stdout != "/s/realign_6.log" || j.Cpus != 4 {
		t.Errorf("job = %+v: squeue's record is kept", j)
	}
	if j.CpuTime == nil || *j.CpuTime != 2*time.Hour+57*time.Minute {
		t.Errorf("cpu time = %v, want the job row's TotalCPU", j.CpuTime)
	}
	if j.PeakMemMB != 11617 {
		t.Errorf("peak mem = %d MB, want the highest MaxRSS of the steps", j.PeakMemMB)
	}
	if j.ExitCode == nil || *j.ExitCode != 0 {
		t.Errorf("exit code = %v, want 0", j.ExitCode)
	}
}

func TestLookupOfAJobOnlySacctKnows(t *testing.T) {
	sacct := sacctRow(map[string]string{"JobID": "9", "State": "FAILED", "ExitCode": "3:0", "Submit": "2026-11-12T10:27:25", "Start": "2026-11-12T10:30:02",
		"End": "2026-11-12T13:32:36", "Elapsed": "03:02:34", "Timelimit": "12:00:00", "Partition": "large", "NodeList": "node03.example.org",
		"AllocTRES": "billing=8,cpu=8,mem=80G,node=1", "TotalCPU": "21:12:08.250", "StdOut": "/s/a_%j.out", "JobName": "align"}) +
		sacctRow(map[string]string{"JobID": "9.batch", "State": "FAILED", "ExitCode": "3:0", "MaxRSS": "81920M"})
	j := lookup(t, newFakeSlurm(t, "", 0, sacct, 0), "9")[0]
	if j.State != JobFailed || j.Failure != FailureNonzeroExit || j.ExitCode == nil || *j.ExitCode != 3 || j.ExitSignal != nil {
		t.Errorf("job = %+v", j)
	}
	if j.Cpus != 8 || j.MemMB != 80*1024 || j.Elapsed != 3*time.Hour+2*time.Minute+34*time.Second || j.TimeLimit != 12*time.Hour {
		t.Errorf("figures = %d cpus, %d MB, %v elapsed, %v limit", j.Cpus, j.MemMB, j.Elapsed, j.TimeLimit)
	}
	if *j.CpuTime != 21*time.Hour+12*time.Minute+8*time.Second+250*time.Millisecond || j.PeakMemMB != 81920 {
		t.Errorf("usage = %v, %d", *j.CpuTime, j.PeakMemMB)
	}
	if j.Source != SourceHistory || j.Stdout != "/s/a_%j.out" || !slices.Equal(j.NodeList, []string{"node03.example.org"}) || j.Name != "align" {
		t.Errorf("job = %+v", j)
	}
}

func TestLookupFailureKinds(t *testing.T) {
	tests := []struct {
		state, exit string
		want        JobState
		failure     string
		hasExit     bool
	}{
		{"FAILED", "3:0", JobFailed, FailureNonzeroExit, true},
		{"TIMEOUT", "0:0", JobFailed, FailureTimeout, false},
		{"OUT_OF_MEMORY", "0:125", JobFailed, FailureOutOfMemory, false},
		{"NODE_FAIL", "1:0", JobFailed, FailureNodeFailure, false},
		{"PREEMPTED", "0:0", JobFailed, FailurePreempted, false},
		{"DEADLINE", "0:0", JobFailed, FailureDeadline, false},
		{"CANCELLED by 1000", "0:0", JobCancelled, "", false},
		{"COMPLETED", "0:0", JobSuccess, "", true},
	}
	for _, tt := range tests {
		sacct := sacctRow(map[string]string{"JobID": "4", "State": tt.state, "ExitCode": tt.exit, "Start": "2026-11-12T10:00:00"})
		j := lookup(t, newFakeSlurm(t, "", 0, sacct, 0), "4")[0]
		if j.State != tt.want || j.Failure != tt.failure || (j.ExitCode != nil) != tt.hasExit {
			t.Errorf("%s %s = state %s, failure %q, exit %v; want %s, %q, has exit %v", tt.state, tt.exit, j.State, j.Failure, j.ExitCode, tt.want, tt.failure, tt.hasExit)
		}
	}
}

func TestLookupExitSignalOfAKilledProgram(t *testing.T) {
	sacct := sacctRow(map[string]string{"JobID": "4", "State": "FAILED", "ExitCode": "0:11", "Start": "2026-11-12T10:00:00"})
	j := lookup(t, newFakeSlurm(t, "", 0, sacct, 0), "4")[0]
	if j.ExitSignal == nil || *j.ExitSignal != 11 || *j.ExitCode != 0 {
		t.Errorf("exit = %v signal %v, want 0 and 11", j.ExitCode, j.ExitSignal)
	}
}

func TestLookupSqueueExitCodeOfAnEndedJob(t *testing.T) {
	out := lookupLine(map[string]string{"ArrayJobID": "6", "ArrayTaskID": "N/A", "State": "FAILED", "Reason": "NonZeroExitCode", "exit_code": "3:0"}) +
		lookupLine(map[string]string{"ArrayJobID": "7", "ArrayTaskID": "N/A", "State": "TIMEOUT", "Reason": "TimeLimit", "exit_code": "0:15"})
	jobs := lookup(t, newFakeSlurm(t, out, 0, "", 0), "6", "7")
	if jobs[0].Failure != FailureNonzeroExit || jobs[0].ExitCode == nil || *jobs[0].ExitCode != 3 {
		t.Errorf("failed = %+v", jobs[0])
	}
	if jobs[1].Failure != FailureTimeout || jobs[1].ExitCode != nil || jobs[1].ExitSignal != nil {
		t.Errorf("a time limit kill has no exit code: %+v", jobs[1])
	}
}

func TestLookupChildOfAFailedParentIsCancelled(t *testing.T) {
	out := lookupLine(map[string]string{"ArrayJobID": "8", "ArrayTaskID": "N/A", "State": "CANCELLED", "Reason": "DependencyNeverSatisfied",
		"Dependency": "afterok:5(failed)"})
	j := lookup(t, newFakeSlurm(t, out, 0, "", 0), "8")[0]
	if j.State != JobCancelled || len(j.DependsOn) != 1 || !j.DependsOn[0].Failed {
		t.Errorf("job = %+v", j)
	}
	if note := jobNote(j); note != "dep failed 5" {
		t.Errorf("note = %q", note)
	}
}

func TestLookupSqueueUnknownIDIsNotAnError(t *testing.T) {
	f := newFakeSlurm(t, "slurm_load_jobs error: Invalid job id specified\n", 1, "", 0)
	jobs := lookup(t, f, "999")
	if len(jobs) != 1 || jobs[0].State != JobNotFound || jobs[0].ID != "999" {
		t.Errorf("jobs = %+v, want the ID as notfound", jobs)
	}
}

func TestLookupSqueueFailureIsAnError(t *testing.T) {
	f := newFakeSlurm(t, "slurm_load_jobs error: Unable to contact slurm controller\n", 1, "", 0)
	if _, err := f.LookupJobs(context.Background(), []string{"5"}); err == nil || !strings.Contains(err.Error(), "Unable to contact") {
		t.Errorf("err = %v, want squeue's message", err)
	}
	if f.count("sacct") != 0 {
		t.Error("sacct must not be asked when squeue failed")
	}
}

func TestLookupWhenSacctFails(t *testing.T) {
	failing := newFakeSlurm(t, "", 0, "sacct: error: slurmdbd: Unable to connect\n", 1)
	if j := lookup(t, failing, "9")[0]; j.State != JobUnknown {
		t.Errorf("state = %s: a failed sacct cannot say the job never existed", j.State)
	}
	off := newFakeSlurm(t, "", 0, "sacct: fatal: Slurm accounting storage is disabled\n", 1)
	if j := lookup(t, off, "9")[0]; j.State != JobNotFound {
		t.Errorf("state = %s: with no accounting the job is not found", j.State)
	}
	out := lookupLine(map[string]string{"ArrayJobID": "6", "ArrayTaskID": "N/A", "State": "COMPLETED", "Name": "x"})
	if j := lookup(t, newFakeSlurm(t, out, 0, "boom\n", 1), "6")[0]; j.State != JobSuccess || j.CpuTime != nil {
		t.Errorf("an ended job squeue holds keeps its state without usage: %+v", j)
	}
}

func TestLookupArrayMergesTheTasksOfBothCalls(t *testing.T) {
	out := lookupLine(map[string]string{"ArrayJobID": "7", "ArrayTaskID": "8", "State": "RUNNING", "Name": "count"}) +
		lookupLine(map[string]string{"ArrayJobID": "7", "ArrayTaskID": "9", "State": "PENDING", "Name": "count"})
	var sacct strings.Builder
	for i := 1; i <= 7; i++ {
		state, exit := "COMPLETED", "0:0"
		if i == 2 {
			state, exit = "FAILED", "1:0"
		}
		if i == 7 {
			state, exit = "CANCELLED by 1", "0:0"
		}
		sacct.WriteString(sacctRow(map[string]string{"JobID": "7_" + strconv.Itoa(i), "State": state, "ExitCode": exit,
			"Submit": "2026-11-12T10:00:00", "Start": fmt.Sprintf("2026-11-12T10:%02d:00", i), "End": fmt.Sprintf("2026-11-12T11:%02d:00", i), "JobName": "count"}))
	}
	sacct.WriteString(sacctRow(map[string]string{"JobID": "7_8", "State": "RUNNING", "Start": "2026-11-12T10:08:00", "End": "Unknown", "JobName": "count"}))
	sacct.WriteString(sacctRow(map[string]string{"JobID": "7_[9-12%2]", "State": "PENDING", "JobName": "count"}))

	f := newFakeSlurm(t, out, 0, sacct.String(), 0)
	tasks := lookup(t, f, "7")
	if len(tasks) != 9 {
		t.Fatalf("%d tasks, want 9 (the range row is not a task)", len(tasks))
	}
	array := GroupArrayTasks(tasks, true)
	if len(array) != 1 {
		t.Fatalf("got %d rows", len(array))
	}
	a := array[0]
	if a.ID != "7" || a.State != JobRunning || a.Source != SourceHistory {
		t.Errorf("array = %+v", a)
	}
	tk := a.Tasks
	if tk.Total() != 9 || len(tk.Success) != 5 || !slices.Equal(tk.Failed, []string{"7_2"}) || !slices.Equal(tk.Cancelled, []string{"7_7"}) ||
		!slices.Equal(tk.Running, []string{"7_8"}) || !slices.Equal(tk.Pending, []string{"7_9"}) {
		t.Errorf("tasks = %+v", tk)
	}
	if !a.EndTime.IsZero() {
		t.Error("an array with a task still in the queue has no end time")
	}
	if a.StartTime.Format("15:04") != "10:01" {
		t.Errorf("start = %v, want the first task's", a.StartTime)
	}
	if f.count("squeue") != 1 || f.count("sacct") != 1 {
		t.Errorf("calls = %v", f.calls())
	}
}

func TestLookupOfOneTaskKeepsTheTaskForm(t *testing.T) {
	sacct := sacctRow(map[string]string{"JobID": "7_2", "State": "FAILED", "ExitCode": "1:0", "Start": "2026-11-12T10:00:00"})
	j := lookup(t, newFakeSlurm(t, "", 0, sacct, 0), "7_2")[0]
	if j.ID != "7_2" || j.ArrayID != "7" || j.TaskID != "2" || !j.IsTask() {
		t.Errorf("job = %+v", j)
	}
}

func TestLookupRunsWithTheCleanEnvironment(t *testing.T) {
	t.Setenv("SACCT_FORMAT", "JobID,State")
	t.Setenv("SQUEUE_STATES", "all")
	f := newFakeSlurm(t, "", 0, "", 0)
	lookup(t, f, "5")
	sacctEnv, _ := os.ReadFile(filepath.Join(f.dir, "env.sacct"))
	squeueEnv, _ := os.ReadFile(filepath.Join(f.dir, "env.squeue"))
	if strings.Contains(string(sacctEnv), "SACCT_FORMAT") || !strings.Contains(string(sacctEnv), "SLURM_TIME_FORMAT=standard") {
		t.Errorf("sacct env = %q", sacctEnv)
	}
	if strings.Contains(string(squeueEnv), "SQUEUE_") {
		t.Errorf("squeue env = %q", squeueEnv)
	}
}

func TestLookupWithNoIDsCallsNothing(t *testing.T) {
	f := newFakeSlurm(t, "", 0, "", 0)
	if jobs, err := f.LookupJobs(context.Background(), nil); err != nil || jobs != nil {
		t.Errorf("jobs = %v, err = %v", jobs, err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "calls")); err == nil {
		t.Error("no ID means no call")
	}
}

func TestParseSacctJobsKeepsABarInTheName(t *testing.T) {
	jobs := parseSacctJobs(sacctRow(map[string]string{"JobID": "4", "State": "COMPLETED", "JobName": "a|b|c"}))
	if len(jobs) != 1 || jobs[0].Name != "a|b|c" {
		t.Errorf("jobs = %+v", jobs)
	}
}

func TestParseSacctJobsUsesTheJobRowsCPUTime(t *testing.T) {
	out := sacctRow(map[string]string{"JobID": "4", "State": "COMPLETED", "Start": "2026-11-12T10:00:00", "TotalCPU": "10:00.000"}) +
		sacctRow(map[string]string{"JobID": "4.batch", "State": "COMPLETED", "TotalCPU": "99:00:00"})
	jobs := parseSacctJobs(out)
	if len(jobs) != 1 || *jobs[0].CpuTime != 10*time.Minute {
		t.Errorf("jobs = %+v", jobs)
	}
}

func TestParseSacctJobsOfAJobThatNeverStartedHasNoCPUTime(t *testing.T) {
	jobs := parseSacctJobs(sacctRow(map[string]string{"JobID": "4", "State": "CANCELLED", "Start": "Unknown", "TotalCPU": "00:00:00", "NodeList": "None assigned",
		"ReqTRES": "billing=1,cpu=1,mem=100M,node=1"}))
	j := jobs[0]
	if j.CpuTime != nil || !j.StartTime.IsZero() || j.NodeList != nil || j.Cpus != 1 || j.MemMB != 100 {
		t.Errorf("job = %+v: the request stands in for the allocation", j)
	}
}

func TestParseSlurmCPUTime(t *testing.T) {
	tests := map[string]time.Duration{
		"31:43.306":      31*time.Minute + 43*time.Second + 306*time.Millisecond,
		"02:57:00":       2*time.Hour + 57*time.Minute,
		"1-02:03:04.500": 26*time.Hour + 3*time.Minute + 4*time.Second + 500*time.Millisecond,
		"00:00:00":       0,
		"":               0,
	}
	for in, want := range tests {
		if got := parseSlurmCPUTime(in); got != want {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
}

func TestIDsNeedingHistory(t *testing.T) {
	queued := []JobInfo{
		{ID: "1", State: JobRunning}, {ID: "2", State: JobSuccess},
		{ID: "3_1", ArrayID: "3", TaskID: "1", State: JobRunning}, {ID: "4_2", ArrayID: "4", TaskID: "2", State: JobPending},
	}
	got := idsNeedingHistory([]string{"1", "2", "3", "4_2", "5"}, queued)
	if want := []string{"2", "3", "5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("need = %v, want %v: an ended job, an array and an unknown ID", got, want)
	}
}
