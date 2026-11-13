package scheduler

import (
	"context"
	"sort"
	"strconv"
	"time"
)

// JobState is where a job is in its life. The words are the ones the jobs table and JSON print.
type JobState string

const (
	JobPending   JobState = "pending"   // waits to start, held jobs included
	JobRunning   JobState = "running"   // runs or is ending, suspended jobs included
	JobSuccess   JobState = "success"   // ended and did what it was asked
	JobFailed    JobState = "failed"    // ended with an error, or the scheduler ended it
	JobCancelled JobState = "cancelled" // a user or the scheduler withdrew it
	JobNotFound  JobState = "notfound"  // the scheduler has no record of the ID
	JobUnknown   JobState = "unknown"   // the scheduler reported a state that has no mapping
)

// JobGpu is a GPU type a job holds or asks for, and how many.
type JobGpu struct {
	Type  string
	Count int
}

// JobDependency is one parent of a job. Type is the scheduler's word for the rule, such as
// DependencyAfterOK or "singleton". JobID is empty for a rule with no parent job.
// Satisfied is true once the rule holds, and Failed is true once it can never hold.
type JobDependency struct {
	Type      string
	JobID     string
	Satisfied bool
	Failed    bool
}

// ArrayTasks lists the tasks of an array by what became of them, as task IDs.
// Other holds the tasks whose state could not be read.
type ArrayTasks struct {
	Running   []string
	Pending   []string
	Success   []string
	Failed    []string
	Cancelled []string
	Other     []string
}

// Total is how many tasks the array has.
func (a ArrayTasks) Total() int {
	return len(a.Running) + len(a.Pending) + len(a.Success) + len(a.Failed) + len(a.Cancelled) + len(a.Other)
}

// Why a job failed. A job the scheduler ended has one of these and no exit code, since the code it would
// carry is the scheduler's own kill.
const (
	FailureNonzeroExit = "nonzero_exit"
	FailureTimeout     = "timeout"
	FailureOutOfMemory = "out_of_memory"
	FailureNodeFailure = "node_failure"
	FailurePreempted   = "preempted"
	FailureDeadline    = "deadline"
)

// Where a JobInfo came from: the queue, the scheduler's record of finished jobs, or a log.
const (
	SourceQueue   = "queue"
	SourceHistory = "history"
)

// JobInfo is a job as the scheduler reports it. A figure the scheduler does not report is left zero.
//   - ID is the form `jobs` takes as an argument. A task of an array is "<array>_<task>", with ArrayID and TaskID set.
//   - An array is one JobInfo with ID and ArrayID the array's and Tasks set. Its Cpus, MemMB and
//     TimeLimit are per task.
//   - Cpus, MemMB and Gpus are the request while a job waits and the allocation once it runs.
//     MemMB is the total over every node.
//   - Elapsed is the time a job has run. TimeLimit is the longest it may run, or TimeUnlimited.
type JobInfo struct {
	ID         string
	ArrayID    string
	TaskID     string
	Name       string
	State      JobState
	Suspended  bool
	Reason     string
	Partitions []string

	NodeList  []string
	NodeCount int
	Cpus      int
	MemMB     int64
	Gpus      []JobGpu

	// Ranks and CpusPerRank describe a job that runs several processes. Both are zero otherwise.
	// RanksPerNode is set only when the user fixed the ranks per node, so every node has that many.
	// Otherwise the scheduler places the ranks, and the split over the nodes is not known.
	Ranks        int
	CpusPerRank  int
	RanksPerNode int

	Elapsed       time.Duration
	TimeLimit     time.Duration
	TimeUnlimited bool
	SubmitTime    time.Time
	StartTime     time.Time
	EndTime       time.Time

	DependsOn []JobDependency
	Stdout    string
	Stderr    string
	Tasks     *ArrayTasks

	// What is known of a job that ended. Failure is set only on a failed job. ExitCode and ExitSignal
	// are set only when the job's own program ended it. CpuTime is the CPU time all its processes used,
	// and PeakMemMB the highest memory one step reached, a lower bound.
	Failure    string
	ExitCode   *int
	ExitSignal *int
	CpuTime    *time.Duration
	PeakMemMB  int64
	Source     string
}

// IsTask reports whether the job is one task of an array.
func (j JobInfo) IsTask() bool { return j.TaskID != "" }

// IsArray reports whether the job is an array standing for its tasks.
func (j JobInfo) IsArray() bool { return j.Tasks != nil }

// ListOptions says how much of each job a listing reads.
// Full asks for the times, the log paths and the process layout, which a table does not show.
type ListOptions struct {
	Full bool
}

// JobLister is a scheduler that lists and looks up jobs.
//   - ListJobs returns the current user's jobs that wait or run, one JobInfo per array task.
//   - LookupJobs returns the jobs the IDs name, also after they left the queue, one JobInfo per array task.
//     An ID the scheduler has no record of is a JobInfo in state JobNotFound, and one it could not
//     look up is JobUnknown.
//   - IsJobID reports whether an argument of `jobs` is a job ID of this scheduler, and not a name filter.
type JobLister interface {
	ListJobs(ctx context.Context, opts ListOptions) ([]JobInfo, error)
	LookupJobs(ctx context.Context, ids []string) ([]JobInfo, error)
	IsJobID(arg string) bool
}

// CancelOutcome is what became of one ID passed to CancelJobs.
type CancelOutcome string

const (
	CancelSent     CancelOutcome = "sent"     // the scheduler took the cancel and reported no error
	CancelNotFound CancelOutcome = "notfound" // the ID is not one of the user's jobs
	CancelError    CancelOutcome = "error"    // the scheduler refused it, and Message says why
)

// CancelResult is the outcome for one ID. Message is the scheduler's own, set for CancelError.
type CancelResult struct {
	ID      string
	Outcome CancelOutcome
	Message string
}

// JobCanceller is a scheduler that cancels jobs.
// CancelJobs cancels the current user's jobs the IDs name with one call and returns a result for each ID, in order.
// An ID that is an array cancels every task, and a task ID cancels that task.
// A failure of the call itself, as opposed to one ID, is an error.
type JobCanceller interface {
	CancelJobs(ctx context.Context, ids []string) ([]CancelResult, error)
}

// GroupArrayTasks folds the task rows of each array into one row that sorts its tasks by what became of them.
//   - The row takes the first task's name and per-task figures, and the earliest submit time.
//   - Its state is running while a task runs, else pending while one waits, else failed when one failed,
//     unknown when one could not be read, cancelled when one was, and success when all succeeded.
//   - complete says the rows hold every task. Then the row has the first task's start and, once every
//     task has ended, the last task's end. A listing sees only the tasks still in the queue, so it has neither.
//
// A job that is not a task is kept as it is.
func GroupArrayTasks(jobs []JobInfo, complete bool) []JobInfo {
	var out []JobInfo
	at := map[string]int{}
	for _, j := range jobs {
		if !j.IsTask() {
			out = append(out, j)
			continue
		}
		i, seen := at[j.ArrayID]
		if !seen {
			array := j
			array.ID, array.TaskID = j.ArrayID, ""
			array.Tasks = &ArrayTasks{}
			array.NodeList, array.NodeCount = nil, 0
			array.Elapsed, array.StartTime, array.EndTime = 0, time.Time{}, time.Time{}
			array.Failure, array.ExitCode, array.ExitSignal, array.CpuTime, array.PeakMemMB = "", nil, nil, nil, 0
			array.Suspended, array.Reason, array.DependsOn = false, "", nil
			array.Source = SourceQueue
			at[j.ArrayID] = len(out)
			out = append(out, array)
			i = len(out) - 1
		}
		array := &out[i]
		array.Tasks.add(j)
		if array.Name == "" {
			array.Name = j.Name
		}
		if !j.SubmitTime.IsZero() && (array.SubmitTime.IsZero() || j.SubmitTime.Before(array.SubmitTime)) {
			array.SubmitTime = j.SubmitTime
		}
		if complete {
			array.Source = SourceHistory
			if !j.StartTime.IsZero() && (array.StartTime.IsZero() || j.StartTime.Before(array.StartTime)) {
				array.StartTime = j.StartTime
			}
			if j.EndTime.After(array.EndTime) {
				array.EndTime = j.EndTime
			}
		}
	}
	for i := range out {
		t := out[i].Tasks
		if t == nil {
			continue
		}
		for _, ids := range [][]string{t.Running, t.Pending, t.Success, t.Failed, t.Cancelled, t.Other} {
			sortTaskIDs(ids)
		}
		out[i].State = t.state()
		if len(t.Running)+len(t.Pending) > 0 {
			out[i].EndTime = time.Time{}
		}
	}
	return out
}

// add files a task under what became of it.
func (a *ArrayTasks) add(j JobInfo) {
	switch j.State {
	case JobRunning:
		a.Running = append(a.Running, j.ID)
	case JobPending:
		a.Pending = append(a.Pending, j.ID)
	case JobSuccess:
		a.Success = append(a.Success, j.ID)
	case JobFailed:
		a.Failed = append(a.Failed, j.ID)
	case JobCancelled:
		a.Cancelled = append(a.Cancelled, j.ID)
	default:
		a.Other = append(a.Other, j.ID)
	}
}

// state is the state of an array from its tasks.
func (a ArrayTasks) state() JobState {
	switch {
	case len(a.Running) > 0:
		return JobRunning
	case len(a.Pending) > 0:
		return JobPending
	case len(a.Failed) > 0:
		return JobFailed
	case len(a.Other) > 0:
		return JobUnknown
	case len(a.Cancelled) > 0:
		return JobCancelled
	}
	return JobSuccess
}

// SortJobs orders jobs by job ID, numbers first by value. The sort is stable.
func SortJobs(jobs []JobInfo) {
	sort.SliceStable(jobs, func(a, b int) bool { return idLess(jobs[a].ID, jobs[b].ID) })
}

// CompareJobIDs orders two job IDs the way SortJobs does: by the number they start with, then by the rest.
func CompareJobIDs(a, b string) int {
	switch {
	case idLess(a, b):
		return -1
	case idLess(b, a):
		return 1
	}
	return 0
}

func sortTaskIDs(ids []string) {
	sort.SliceStable(ids, func(a, b int) bool { return idLess(ids[a], ids[b]) })
}

// idLess compares job IDs by the number they start with, then by the rest as text.
// "245068_4" follows "245068_3" and comes before "245068_10", and "9" comes before "10".
func idLess(a, b string) bool {
	na, ra := splitLeadingNumber(a)
	nb, rb := splitLeadingNumber(b)
	if na != nb {
		return na < nb
	}
	ta, tb := splitTaskSuffix(ra), splitTaskSuffix(rb)
	if ta != tb {
		return ta < tb
	}
	return ra < rb
}

// splitLeadingNumber returns the digits an ID starts with as a number, and the rest.
// An ID with no leading digits sorts after every number.
func splitLeadingNumber(id string) (int64, string) {
	end := 0
	for end < len(id) && id[end] >= '0' && id[end] <= '9' {
		end++
	}
	if end == 0 {
		return 1<<62 - 1, id
	}
	n, err := strconv.ParseInt(id[:end], 10, 64)
	if err != nil {
		return 1<<62 - 1, id
	}
	return n, id[end:]
}

// splitTaskSuffix returns the number of a "_<task>" suffix, or -1 when the rest has none.
func splitTaskSuffix(rest string) int64 {
	if len(rest) < 2 || rest[0] != '_' {
		return -1
	}
	n, err := strconv.ParseInt(rest[1:], 10, 64)
	if err != nil {
		return -1
	}
	return n
}
