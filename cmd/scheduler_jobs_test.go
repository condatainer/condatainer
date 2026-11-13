package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
)

// fakeJobScheduler lists the jobs it is given and knows no SLURM word. The embedded interface is nil,
// so a call to any other method fails the test loudly.
type fakeJobScheduler struct {
	scheduler.Scheduler
	jobs   []scheduler.JobInfo
	err    error
	calls  int
	full   bool
	lookup func(ids []string) []scheduler.JobInfo
	looked [][]string

	// cancel answers CancelJobs. The default sends every ID.
	cancel    func(ids []string) ([]scheduler.CancelResult, error)
	cancelled [][]string
}

func (f *fakeJobScheduler) CancelJobs(_ context.Context, ids []string) ([]scheduler.CancelResult, error) {
	f.cancelled = append(f.cancelled, ids)
	if f.cancel != nil {
		return f.cancel(ids)
	}
	results := make([]scheduler.CancelResult, len(ids))
	for i, id := range ids {
		results[i] = scheduler.CancelResult{ID: id, Outcome: scheduler.CancelSent}
	}
	return results, nil
}

// listsOnly lists and looks up jobs but cannot cancel them.
type listsOnly struct {
	scheduler.Scheduler
	inner *fakeJobScheduler
}

func (l listsOnly) GetType() scheduler.SchedulerType { return "PBS" }
func (l listsOnly) GetBinary() string                { return "/fake/qsub" }
func (l listsOnly) IsAvailable() bool                { return true }
func (l listsOnly) IsInsideJob() bool                { return false }
func (l listsOnly) IsJobID(a string) bool            { return l.inner.IsJobID(a) }
func (l listsOnly) ListJobs(ctx context.Context, o scheduler.ListOptions) ([]scheduler.JobInfo, error) {
	return l.inner.ListJobs(ctx, o)
}
func (l listsOnly) LookupJobs(ctx context.Context, ids []string) ([]scheduler.JobInfo, error) {
	return l.inner.LookupJobs(ctx, ids)
}

func (f *fakeJobScheduler) GetType() scheduler.SchedulerType { return "FAKE" }
func (f *fakeJobScheduler) GetBinary() string                { return "/fake/bin/fsub" }
func (f *fakeJobScheduler) IsAvailable() bool                { return true }
func (f *fakeJobScheduler) IsInsideJob() bool                { return false }
func (f *fakeJobScheduler) IsJobID(arg string) bool {
	return len(arg) > 0 && strings.Trim(arg, "0123456789_") == ""
}
func (f *fakeJobScheduler) ListJobs(_ context.Context, opts scheduler.ListOptions) ([]scheduler.JobInfo, error) {
	f.calls++
	f.full = opts.Full
	return f.jobs, f.err
}

func (f *fakeJobScheduler) LookupJobs(_ context.Context, ids []string) ([]scheduler.JobInfo, error) {
	f.calls++
	f.looked = append(f.looked, ids)
	if f.lookup == nil {
		return nil, f.err
	}
	return f.lookup(ids), f.err
}

// notALister is a scheduler without job listing.
type notALister struct{ scheduler.Scheduler }

func (notALister) GetType() scheduler.SchedulerType { return "PBS" }

func fakeQueue() []scheduler.JobInfo {
	task := func(id string, state scheduler.JobState) scheduler.JobInfo {
		return scheduler.JobInfo{ID: "30_" + id, ArrayID: "30", TaskID: id, Name: "Count", State: state, Cpus: 4, MemMB: 16384, TimeLimit: time.Hour, Source: scheduler.SourceQueue}
	}
	return []scheduler.JobInfo{
		{ID: "21", Name: "align_sample", State: scheduler.JobRunning, Cpus: 8, MemMB: 65536, TimeLimit: 12 * time.Hour, NodeList: []string{"n1"}, NodeCount: 1, Source: scheduler.SourceQueue},
		{ID: "9", Name: "Align_Index", State: scheduler.JobPending, Cpus: 2, MemMB: 4096, TimeLimit: time.Hour, Reason: "Priority", Source: scheduler.SourceQueue},
		task("1", scheduler.JobRunning), task("2", scheduler.JobPending), task("3", scheduler.JobPending),
	}
}

// runJobs runs `scheduler jobs` against sched and returns what it printed.
func runJobs(t *testing.T, sched scheduler.Scheduler, args ...string) (string, error) {
	t.Helper()
	scheduler.SetActiveScheduler(sched)
	t.Cleanup(scheduler.ClearActiveScheduler)
	schedulerJobsTasks, schedulerJobsJSON = false, false
	t.Cleanup(func() { schedulerJobsTasks, schedulerJobsJSON = false, false })

	c := schedulerJobsCmd
	c.SetContext(context.Background())
	var out bytes.Buffer
	c.SetOut(&out)
	var rest []string
	for _, a := range args {
		switch a {
		case "--tasks":
			schedulerJobsTasks = true
		case "--json":
			schedulerJobsJSON = true
		default:
			rest = append(rest, a)
		}
	}
	err := runSchedulerJobs(c, rest)
	return out.String(), err
}

// rowIDs is the job IDs of a table, or the one ID a detail block starts with.
func rowIDs(out string) []string {
	if !strings.HasPrefix(out, "JOBID") {
		return []string{strings.Fields(out)[0]}
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n")[1:] {
		ids = append(ids, strings.Fields(line)[0])
	}
	return ids
}

func TestJobsListsTheQueueSortedWithArraysFolded(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	out, err := runJobs(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(out); strings.Join(got, " ") != "9 21 30" {
		t.Errorf("rows = %v, want 9 21 30 in job ID order", got)
	}
	if !strings.Contains(out, "1 running, 2 p") {
		t.Errorf("the array is one row with its task counts:\n%s", out)
	}
	if f.calls != 1 {
		t.Errorf("%d listings, want one", f.calls)
	}
}

func TestJobsTasksListsEachTask(t *testing.T) {
	out, err := runJobs(t, &fakeJobScheduler{jobs: fakeQueue()}, "--tasks")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(out); strings.Join(got, " ") != "9 21 30_1 30_2 30_3" {
		t.Errorf("rows = %v", got)
	}
}

func TestJobsNameFiltersAreCaseInsensitiveSubstringsAndAnd(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"align"}, "9 21"},
		{[]string{"ALIGN", "index"}, "9"},
		{[]string{"align", "count"}, ""},
		{[]string{"nomatch"}, ""},
	} {
		out, err := runJobs(t, f, tt.args...)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(rowIDs(out), " "); got != tt.want {
			t.Errorf("filters %v = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestJobsNothingMatchingPrintsTheHeaderAndSucceeds(t *testing.T) {
	out, err := runJobs(t, &fakeJobScheduler{jobs: nil})
	if err != nil || strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "JOBID") {
		t.Errorf("out = %q, err = %v, want the header only", out, err)
	}
}

// lookupInQueue answers a lookup from fakeQueue, the way a scheduler that still holds every job would.
func lookupInQueue(ids []string) []scheduler.JobInfo {
	var rows []scheduler.JobInfo
	for _, id := range ids {
		found := false
		for _, j := range fakeQueue() {
			if j.ID == id || j.ArrayID == id {
				rows = append(rows, j)
				found = true
			}
		}
		if !found {
			rows = append(rows, scheduler.JobInfo{ID: id, State: scheduler.JobNotFound})
		}
	}
	return rows
}

func TestJobsIDsSelectJobsAndTasks(t *testing.T) {
	f := &fakeJobScheduler{lookup: lookupInQueue}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"21"}, "21"},
		{[]string{"9", "21"}, "9 21"},
		{[]string{"30"}, "30"},
		{[]string{"30_2"}, "30_2"},
		{[]string{"30_2", "21"}, "21 30_2"},
		{[]string{"30", "30_2"}, "30"},
	} {
		out, err := runJobs(t, f, tt.args...)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(rowIDs(out), " "); got != tt.want {
			t.Errorf("IDs %v = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestJobsRefusesIDsMixedWithNames(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	if _, err := runJobs(t, f, "21", "align"); !errors.Is(err, errMixedJobArgs) {
		t.Errorf("err = %v, want %v", err, errMixedJobArgs)
	}
	if f.calls != 0 {
		t.Error("a refused call must not reach the scheduler")
	}
}

func TestJobsFailedListingIsAnError(t *testing.T) {
	f := &fakeJobScheduler{err: errors.New("Unable to contact slurm controller")}
	out, err := runJobs(t, f)
	if err == nil || !strings.Contains(err.Error(), "Unable to contact") {
		t.Errorf("err = %v, want the scheduler's message", err)
	}
	if out != "" {
		t.Errorf("a failed listing printed %q, never an empty table", out)
	}
}

func TestJobsOnASchedulerWithoutListing(t *testing.T) {
	_, err := runJobs(t, notALister{})
	if err == nil || err.Error() != "scheduler pbs is not supported" {
		t.Errorf("err = %v, want the scheduler named as not supported", err)
	}
}

func jsonJobs(t *testing.T, out string) []map[string]any {
	t.Helper()
	var doc struct {
		SchemaVersion int              `json:"schema_version"`
		Scheduler     map[string]any   `json:"scheduler"`
		Jobs          []map[string]any `json:"jobs"`
		Problems      []map[string]any `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if doc.SchemaVersion != 1 || doc.Scheduler["type"] != "fake" || len(doc.Problems) != 0 {
		t.Errorf("document header = %+v", doc)
	}
	return doc.Jobs
}

func TestJobsJSONRecords(t *testing.T) {
	submit := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	queue := fakeQueue()
	queue[0].SubmitTime, queue[0].StartTime = submit, submit.Add(90*time.Second)
	queue[0].Elapsed = 2 * time.Hour
	queue[0].Gpus = []scheduler.JobGpu{{Type: "h100", Count: 1}}
	queue[0].Stdout, queue[0].Stderr = "/s/a.out", "/s/a.out"

	out, err := runJobs(t, &fakeJobScheduler{jobs: queue}, "--json")
	if err != nil {
		t.Fatal(err)
	}
	jobs := jsonJobs(t, out)
	if len(jobs) != 3 {
		t.Fatalf("%d records, want 9, 21 and the array", len(jobs))
	}

	running := jobs[1]
	if running["id"] != "21" || running["state"] != "running" || running["source"] != "queue" {
		t.Errorf("running = %v", running)
	}
	if running["elapsed_s"] != float64(7200) || running["queued_s"] != float64(90) || running["time_limit_s"] != float64(43200) {
		t.Errorf("seconds = %v", running)
	}
	if running["submit_time"] != "2026-11-12T10:00:00Z" || running["start_time"] != "2026-11-12T10:01:30Z" {
		t.Errorf("times = %v", running)
	}
	if res := running["resources"].(map[string]any); res["cpus"] != float64(8) || res["mem_mb"] != float64(65536) {
		t.Errorf("resources = %v", res)
	}
	if running["stdout"] != "/s/a.out" || running["stderr"] != "/s/a.out" {
		t.Errorf("logs = %v", running)
	}

	pending := jobs[0]
	for _, key := range []string{"start_time", "end_time", "elapsed_s", "queued_s", "node_list"} {
		if _, ok := pending[key]; ok {
			t.Errorf("pending job has %q, want it left out", key)
		}
	}
	if pending["reason"] != "Priority" {
		t.Errorf("pending = %v", pending)
	}

	array := jobs[2]
	tasks := array["tasks"].(map[string]any)
	if array["id"] != "30" || tasks["running"] != float64(1) || tasks["pending"] != float64(2) {
		t.Errorf("array = %v", array)
	}
	if ids := tasks["pending_ids"].([]any); len(ids) != 2 || ids[0] != "30_2" {
		t.Errorf("pending ids = %v", ids)
	}
	for _, key := range []string{"elapsed_s", "start_time", "end_time", "array_id", "task_id"} {
		if _, ok := array[key]; ok {
			t.Errorf("array has %q, want it left out", key)
		}
	}
}

func TestJobsJSONTasksCarryTheirArray(t *testing.T) {
	out, err := runJobs(t, &fakeJobScheduler{jobs: fakeQueue()}, "--json", "--tasks")
	if err != nil {
		t.Fatal(err)
	}
	jobs := jsonJobs(t, out)
	if len(jobs) != 5 {
		t.Fatalf("%d records, want one per task", len(jobs))
	}
	if task := jobs[3]; task["id"] != "30_2" || task["array_id"] != "30" || task["task_id"] != "2" {
		t.Errorf("task = %v", task)
	}
	if _, ok := jobs[3]["tasks"]; ok {
		t.Error("a task is not an array")
	}
}

func TestJobsJSONUnlimitedAndLists(t *testing.T) {
	q := []scheduler.JobInfo{{ID: "5", Name: "x", State: scheduler.JobPending, TimeUnlimited: true}}
	out, _ := runJobs(t, &fakeJobScheduler{jobs: q}, "--json")
	job := jsonJobs(t, out)[0]
	if job["time_unlimited"] != true {
		t.Errorf("job = %v", job)
	}
	if _, ok := job["time_limit_s"]; ok {
		t.Error("no limit figure when there is no limit")
	}
	if p, ok := job["partitions"].([]any); !ok || len(p) != 0 {
		t.Errorf("partitions = %v, want []", job["partitions"])
	}
	if g := job["resources"].(map[string]any)["gpus"]; g == nil {
		t.Error("gpus must be [], never null")
	}
}

func TestJobsJSONEmptyQueueIsAnEmptyList(t *testing.T) {
	out, err := runJobs(t, &fakeJobScheduler{}, "--json")
	if err != nil || !strings.Contains(out, `"jobs": []`) {
		t.Errorf("out = %s, err = %v", out, err)
	}
}

func TestJobsJSONFailedListingWritesTheProblemAndFails(t *testing.T) {
	out, err := runJobs(t, &fakeJobScheduler{err: errors.New("Unable to contact slurm controller")}, "--json")
	if err == nil {
		t.Error("a failed listing must exit non-zero in JSON too")
	}
	var doc struct {
		Jobs     []any            `json:"jobs"`
		Problems []map[string]any `json:"problems"`
	}
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil || len(doc.Problems) != 1 ||
		!strings.Contains(doc.Problems[0]["message"].(string), "Unable to contact") || len(doc.Jobs) != 0 {
		t.Errorf("document = %s (%v)", out, jerr)
	}
}

func TestJobsAskForFullFieldsOnlyForJSON(t *testing.T) {
	f := &fakeJobScheduler{}
	_, _ = runJobs(t, f)
	if f.full {
		t.Error("a table does not ask for the log paths and times")
	}
	_, _ = runJobs(t, f, "--json")
	if !f.full {
		t.Error("--json asks for the full fields")
	}
}

func TestJobsByIDLooksUpOnceAndNeverLists(t *testing.T) {
	f := &fakeJobScheduler{lookup: lookupInQueue}
	if _, err := runJobs(t, f, "9", "21", "30_2"); err != nil {
		t.Fatal(err)
	}
	if len(f.looked) != 1 || strings.Join(f.looked[0], " ") != "9 21 30_2" || f.calls != 1 {
		t.Errorf("lookups = %v, calls = %d, want one lookup of all three IDs", f.looked, f.calls)
	}
	if _, err := runJobs(t, f, "align"); err != nil || len(f.looked) != 1 {
		t.Errorf("a name filter must list, not look up: %v", f.looked)
	}
}

func TestJobsOneIDShowsTheDetailAndSeveralShowTheTable(t *testing.T) {
	f := &fakeJobScheduler{lookup: lookupInQueue}
	one, _ := runJobs(t, f, "21")
	if strings.HasPrefix(one, "JOBID") || !strings.HasPrefix(one, "21  running  align_sample\n") || !strings.Contains(one, "  resources   8 cpus, 64 GB") {
		t.Errorf("one ID:\n%s", one)
	}
	many, _ := runJobs(t, f, "21", "9")
	if !strings.HasPrefix(many, "JOBID") {
		t.Errorf("two IDs:\n%s", many)
	}
	tasks, _ := runJobs(t, f, "--tasks", "30")
	if !strings.HasPrefix(tasks, "JOBID") || len(rowIDs(tasks)) != 3 {
		t.Errorf("an array with --tasks is a row per task:\n%s", tasks)
	}
}

func TestJobsNotFoundIsAStateNotAnError(t *testing.T) {
	f := &fakeJobScheduler{lookup: lookupInQueue}
	out, err := runJobs(t, f, "999")
	if err != nil || strings.TrimSpace(out) != "999  notfound" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	out, _ = runJobs(t, f, "999", "21")
	if !strings.Contains(out, "999") || !strings.Contains(out, "notfound") || strings.HasPrefix(out, "999") {
		t.Errorf("table:\n%s", out)
	}
}

func TestJobsJSONOfALookupHasTheFinishedFields(t *testing.T) {
	cpu := 3 * time.Hour
	code, sig := 3, 9
	f := &fakeJobScheduler{lookup: func(ids []string) []scheduler.JobInfo {
		return []scheduler.JobInfo{
			{ID: "5", Name: "a", State: scheduler.JobFailed, Failure: scheduler.FailureNonzeroExit, ExitCode: &code, ExitSignal: &sig,
				CpuTime: &cpu, PeakMemMB: 2048, Source: scheduler.SourceHistory},
			{ID: "6", State: scheduler.JobNotFound},
		}
	}}
	out, err := runJobs(t, f, "--json", "5", "6")
	if err != nil {
		t.Fatal(err)
	}
	jobs := jsonJobs(t, out)
	failed := jobs[0]
	if failed["failure"] != "nonzero_exit" || failed["exit_code"] != float64(3) || failed["exit_signal"] != float64(9) || failed["source"] != "history" {
		t.Errorf("failed = %v", failed)
	}
	if usage := failed["usage"].(map[string]any); usage["cpu_time_s"] != float64(10800) || usage["peak_mem_mb"] != float64(2048) {
		t.Errorf("usage = %v", failed["usage"])
	}
	missing := jobs[1]
	if missing["state"] != "notfound" {
		t.Errorf("missing = %v", missing)
	}
	for _, key := range []string{"source", "failure", "exit_code", "usage", "start_time"} {
		if _, ok := missing[key]; ok {
			t.Errorf("an unknown ID has %q", key)
		}
	}
}

func TestJobsJSONOfAnArrayLookupHasEveryTask(t *testing.T) {
	task := func(id string, state scheduler.JobState) scheduler.JobInfo {
		return scheduler.JobInfo{ID: "7_" + id, ArrayID: "7", TaskID: id, Name: "count", State: state, Source: scheduler.SourceHistory,
			SubmitTime: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	}
	t1, t2 := task("1", scheduler.JobSuccess), task("2", scheduler.JobFailed)
	t1.StartTime, t1.EndTime = t1.SubmitTime.Add(time.Minute), t1.SubmitTime.Add(10*time.Minute)
	t2.StartTime, t2.EndTime = t1.SubmitTime.Add(2*time.Minute), t1.SubmitTime.Add(20*time.Minute)
	f := &fakeJobScheduler{lookup: func([]string) []scheduler.JobInfo {
		return []scheduler.JobInfo{t1, t2, task("3", scheduler.JobCancelled)}
	}}
	out, _ := runJobs(t, f, "--json", "7")
	array := jsonJobs(t, out)[0]
	tasks := array["tasks"].(map[string]any)
	if array["id"] != "7" || array["state"] != "failed" || tasks["total"] != float64(3) || tasks["success"] != float64(1) ||
		tasks["failed"] != float64(1) || tasks["cancelled"] != float64(1) {
		t.Errorf("array = %v", array)
	}
	if ids := tasks["failed_ids"].([]any); len(ids) != 1 || ids[0] != "7_2" {
		t.Errorf("failed ids = %v", ids)
	}
	if array["start_time"] != "2026-11-12T10:01:00Z" || array["end_time"] != "2026-11-12T10:20:00Z" || array["queued_s"] != float64(60) {
		t.Errorf("times = %v", array)
	}
	if _, ok := array["elapsed_s"]; ok {
		t.Error("an array has no elapsed")
	}
}

func TestJobsJSONLayoutHasRanksPerNodeOnlyWhenFixed(t *testing.T) {
	f := &fakeJobScheduler{jobs: []scheduler.JobInfo{
		{ID: "1", Name: "a", State: scheduler.JobRunning, NodeCount: 2, Ranks: 4, RanksPerNode: 2, CpusPerRank: 2},
		{ID: "2", Name: "b", State: scheduler.JobRunning, NodeCount: 2, Ranks: 4, CpusPerRank: 2},
	}}
	out, err := runJobs(t, f, "--json")
	if err != nil {
		t.Fatal(err)
	}
	jobs := jsonJobs(t, out)
	fixed, placed := jobs[0]["resources"].(map[string]any), jobs[1]["resources"].(map[string]any)
	if fixed["ranks"] != float64(4) || fixed["ranks_per_node"] != float64(2) || fixed["cpus_per_rank"] != float64(2) {
		t.Errorf("fixed = %v", fixed)
	}
	if _, ok := placed["ranks_per_node"]; ok || placed["ranks"] != float64(4) {
		t.Errorf("placed by the scheduler = %v, want ranks and no split", placed)
	}
}
