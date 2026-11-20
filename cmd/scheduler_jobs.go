package cmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/spf13/cobra"
)

var (
	schedulerJobsTasks   bool
	schedulerJobsDetail  bool
	schedulerJobsNoTrunc bool
	schedulerJobsJSON    bool
)

// jobsTableWidth is the width the jobs table fits. Tests replace it.
var jobsTableWidth = writerWidth

var schedulerJobsCmd = &cobra.Command{
	SilenceUsage: true, // Runtime errors should not show usage
	Use:          "jobs [flags] [<id>...|<name>...]",
	Short:        "List your jobs, or look one up by ID",
	Long: `List your jobs that wait or run, one row each. With job IDs, look those jobs up.

Arguments are all job IDs, or all name filters. Mixing them is refused.
A name filter is part of the job name, in any case. It sees only the queue.
An ID is a job (245068) or one array task (245068_4). It finds ended jobs too.

The table has a row for each job. An array is one row with its tasks counted.
- STATE is pending, running, success, failed or cancelled.
- CPUS, GPUS and MEM are what a job asked for, then what it holds once running.
- TIME is HH:MM:SS: the limit while a job waits, then the time used.
- NODE is where the job runs.
- NOTE is why it waits, what it waits for, or why it failed.

In a cell:
- A - is a figure the scheduler does not report.
- inf in TIME means no limit.
- A … ends a value cut to fit the terminal. Piped output is never cut.
- --no-trunc keeps every value whole in a terminal.
- An ID can also have the state notfound or unknown.

One ID shows the job in full: a line for each fact the scheduler reported.
A finished job has its exit code, CPU time and peak memory.
A failed job also has why it failed.
An array lists its tasks by state, and the IDs of the tasks that failed.`,
	Example: `  condatainer scheduler jobs
  condatainer scheduler jobs align   # Jobs with "align" in the name
  condatainer scheduler jobs 245068  # One job in full, even after it ended
  condatainer scheduler jobs 245068 245070     # Several jobs as rows
  condatainer scheduler jobs --tasks 245080
  condatainer scheduler jobs --detail
  condatainer scheduler jobs --no-trunc
  condatainer scheduler jobs --host            # Inside a container`,
	RunE: runSchedulerJobs,
}

// lookupJobLister finds the scheduler that lists jobs. A scheduler that was found but cannot list is returned
// with the error, and none found is nil.
func lookupJobLister() (scheduler.Scheduler, scheduler.JobLister, error) {
	sched, err := lookupQueryScheduler()
	if err != nil {
		return nil, nil, err
	}
	lister, ok := sched.(scheduler.JobLister)
	if !ok {
		return sched, nil, fmt.Errorf("scheduler %s is not supported", strings.ToLower(string(sched.GetType())))
	}
	return sched, lister, nil
}

var errMixedJobArgs = errors.New("give job IDs or name filters, not both")

// splitJobArgs sorts the arguments into job IDs and name filters. Giving both is an error.
func splitJobArgs(args []string, isID func(string) bool) (ids, names []string, err error) {
	for _, a := range args {
		if isID(a) {
			ids = append(ids, a)
		} else {
			names = append(names, a)
		}
	}
	if len(ids) > 0 && len(names) > 0 {
		return nil, nil, errMixedJobArgs
	}
	return ids, names, nil
}

// selectJobs keeps the jobs the arguments name, before arrays are folded.
//   - An ID selects the job, and the tasks of an array of that ID.
//   - A name filter keeps a job whose name holds every filter, in any case.
func selectJobs(jobs []scheduler.JobInfo, ids, names []string) []scheduler.JobInfo {
	if len(ids) == 0 && len(names) == 0 {
		return jobs
	}
	var kept []scheduler.JobInfo
	for _, j := range jobs {
		var match bool
		if len(ids) > 0 {
			match = slices.Contains(ids, j.ID) || slices.Contains(ids, j.ArrayID)
		} else {
			lower := strings.ToLower(j.Name)
			match = !slices.ContainsFunc(names, func(n string) bool { return !strings.Contains(lower, strings.ToLower(n)) })
		}
		if match {
			kept = append(kept, j)
		}
	}
	return kept
}

// foldArrays folds the tasks of each array into one row, except the tasks an ID named on its own.
// complete says the tasks are every task of the array, as a lookup by ID gives.
func foldArrays(jobs []scheduler.JobInfo, ids []string, complete bool) []scheduler.JobInfo {
	var alone, rest []scheduler.JobInfo
	for _, j := range jobs {
		if j.IsTask() && slices.Contains(ids, j.ID) && !slices.Contains(ids, j.ArrayID) {
			alone = append(alone, j)
		} else {
			rest = append(rest, j)
		}
	}
	return append(scheduler.GroupArrayTasks(rest, complete), alone...)
}

// jobRecord describes a job for the JSON document.
func jobRecord(j scheduler.JobInfo) report.Job {
	rec := report.Job{
		ID: j.ID, State: string(j.State), Suspended: j.Suspended, Name: j.Name,
		Partitions: report.List[string](j.Partitions), Reason: j.Reason,
		NodeList: report.List[string](j.NodeList), TimeUnlimited: j.TimeUnlimited,
		TimeLimitS: int64(j.TimeLimit.Seconds()),
		Stdout:     j.Stdout, Stderr: j.Stderr, Source: j.Source,
		Resources: report.JobResources{
			Cpus: j.Cpus, MemMB: j.MemMB, Nodes: j.NodeCount, Gpus: report.List[report.JobGpu]{},
			Ranks: j.Ranks, CpusPerRank: j.CpusPerRank, RanksPerNode: j.RanksPerNode,
		},
	}
	if j.IsTask() {
		rec.ArrayID, rec.TaskID = j.ArrayID, j.TaskID
	}
	for _, g := range j.Gpus {
		rec.Resources.Gpus = append(rec.Resources.Gpus, report.JobGpu{Type: g.Type, Count: g.Count})
	}
	for _, d := range j.DependsOn {
		rec.DependsOn = append(rec.DependsOn, report.JobDependency{Type: d.Type, JobID: d.JobID, Satisfied: d.Satisfied, Failed: d.Failed})
	}
	rfc := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format(time.RFC3339)
	}
	rec.SubmitTime = rfc(j.SubmitTime)

	rec.StartTime, rec.EndTime = rfc(j.StartTime), rfc(j.EndTime)
	if !j.StartTime.IsZero() && !j.SubmitTime.IsZero() {
		queued := int64(j.StartTime.Sub(j.SubmitTime).Seconds())
		rec.QueuedS = &queued
	}
	if j.IsArray() {
		rec.Tasks = arrayTasksRecord(j)
		return rec
	}
	if !j.StartTime.IsZero() {
		elapsed := int64(j.Elapsed.Seconds())
		rec.ElapsedS = &elapsed
	}

	rec.Failure = j.Failure
	rec.ExitCode, rec.ExitSignal = j.ExitCode, j.ExitSignal
	if j.CpuTime != nil || j.PeakMemMB > 0 {
		rec.Usage = &report.JobUsage{PeakMemMB: j.PeakMemMB}
		if j.CpuTime != nil {
			cpu := int64(j.CpuTime.Seconds())
			rec.Usage.CpuTimeS = &cpu
		}
	}
	return rec
}

// arrayTasksRecord is the tasks of an array. A record read from the queue lists only the tasks that wait or run.
func arrayTasksRecord(j scheduler.JobInfo) *report.JobTasks {
	t := j.Tasks
	rec := &report.JobTasks{
		Running: len(t.Running), Pending: len(t.Pending),
		RunningIDs: report.List[string](t.Running), PendingIDs: report.List[string](t.Pending),
	}
	if j.Source != scheduler.SourceHistory {
		return rec
	}
	total, success, failed, cancelled := t.Total(), len(t.Success), len(t.Failed), len(t.Cancelled)
	rec.Total, rec.Success, rec.Failed, rec.Cancelled = &total, &success, &failed, &cancelled
	rec.SuccessIDs, rec.FailedIDs = report.List[string](t.Success), report.List[string](t.Failed)
	rec.CancelledIDs, rec.UnknownIDs = report.List[string](t.Cancelled), report.List[string](t.Other)
	return rec
}

func runSchedulerJobs(cmd *cobra.Command, args []string) error {
	if done, err := runOnHostIfAsked(cmd, args); done {
		return err
	}
	sched, lister, err := lookupJobLister()
	if err != nil {
		return jobsFailure(cmd, sched, err)
	}
	ids, names, err := splitJobArgs(args, lister.IsJobID)
	if err != nil {
		return err
	}

	var jobs []scheduler.JobInfo
	if len(ids) > 0 {
		jobs, err = lister.LookupJobs(cmd.Context(), ids)
	} else {
		jobs, err = lister.ListJobs(cmd.Context(), scheduler.ListOptions{Full: schedulerJobsJSON || schedulerJobsDetail})
		jobs = selectJobs(jobs, nil, names)
	}
	if err != nil {
		return jobsFailure(cmd, sched, err)
	}
	if !schedulerJobsTasks {
		jobs = foldArrays(jobs, ids, len(ids) > 0)
	}
	scheduler.SortJobs(jobs)

	out := cmd.OutOrStdout()
	switch {
	case schedulerJobsJSON:
		doc := &report.Jobs{Scheduler: schedulerRecord(sched, ""), Jobs: report.List[report.Job]{}, Problems: report.List[report.Problem]{}}
		for _, j := range jobs {
			doc.Jobs = append(doc.Jobs, jobRecord(j))
		}
		return report.Write(out, doc)
	case schedulerJobsDetail || (len(ids) == 1 && len(jobs) == 1):
		for i, j := range jobs {
			if i > 0 {
				fmt.Fprintln(out)
			}
			scheduler.WriteJobDetail(out, j)
		}
	default:
		width := jobsTableWidth(out)
		if schedulerJobsNoTrunc {
			width = 0
		}
		scheduler.WriteJobTable(out, jobs, width)
	}
	return nil
}

// jobsFailure reports a call that could not list jobs. A nil sched is none found. With --json the document carries the problem
// and the error is returned after it, so the exit status is not zero.
func jobsFailure(cmd *cobra.Command, sched scheduler.Scheduler, err error) error {
	if !schedulerJobsJSON {
		return err
	}
	doc := &report.Jobs{Jobs: report.List[report.Job]{}, Problems: report.List[report.Problem]{{Message: err.Error()}}}
	doc.Scheduler = schedulerRecord(sched, "")
	if werr := report.Write(cmd.OutOrStdout(), doc); werr != nil {
		return werr
	}
	return err
}

func init() {
	schedulerJobsCmd.Flags().BoolVar(&schedulerJobsTasks, "tasks", false, "List each task of an array")
	schedulerJobsCmd.Flags().BoolVar(&schedulerJobsDetail, "detail", false, "Show each job in full")
	schedulerJobsCmd.Flags().BoolVar(&schedulerJobsNoTrunc, "no-trunc", false, "Do not cut long values")
	jsonFlag(schedulerJobsCmd.Flags(), &schedulerJobsJSON)
	schedulerJobsCmd.MarkFlagsMutuallyExclusive("detail", "json")
	addHostFlags(schedulerJobsCmd.Flags())
}
