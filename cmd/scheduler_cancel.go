package cmd

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var schedulerCancelJSON bool

// haveTerminal and confirmCancel are the terminal check and the question, so a test can answer them.
var (
	haveTerminal  = utils.IsInteractiveShell
	confirmCancel = utils.Confirm
)

var errSomeNotCancelled = errors.New("some jobs were not cancelled")

var schedulerCancelCmd = &cobra.Command{
	SilenceUsage: true, // Runtime errors should not show usage
	Use:          "cancel <id>...|<name>...",
	Args:         cobra.MinimumNArgs(1),
	Short:        "Cancel your jobs by ID or by name",
	Long: `Cancel your own jobs that wait or run. Jobs of other users are never touched.

Arguments are all job IDs, or all name filters, as for jobs.

- An array ID cancels every task. A task ID (245068_4) cancels one.
- IDs cancel at once. A name asks first.
- An ID that is not your job in the queue is not found, and the exit status is not zero.`,
	Example: `  condatainer scheduler cancel 245068 245070
  condatainer scheduler cancel 245080        # Every task of the array
  condatainer scheduler cancel 245080_4      # One task
  condatainer scheduler cancel align         # Asks first
  condatainer scheduler cancel align -y`,
	RunE: runSchedulerCancel,
}

// cancelPick is a job the command picked, and what happened to it. Key is the ID passed to the scheduler.
type cancelPick struct {
	job     scheduler.JobInfo
	key     string
	outcome scheduler.CancelOutcome
	message string
}

func runSchedulerCancel(cmd *cobra.Command, args []string) error {
	sched, lister, err := lookupJobLister()
	if err != nil {
		return cancelFailure(cmd, nil, err)
	}
	canceller, ok := sched.(scheduler.JobCanceller)
	if !ok {
		return cancelFailure(cmd, sched, fmt.Errorf("scheduler %s is not supported", strings.ToLower(string(sched.GetType()))))
	}
	ids, names, err := splitJobArgs(args, lister.IsJobID)
	if err != nil {
		return err
	}

	listed, err := lister.ListJobs(cmd.Context(), scheduler.ListOptions{Full: schedulerCancelJSON})
	if err != nil {
		return cancelFailure(cmd, sched, err)
	}
	picks, send := pickToCancel(listed, ids, names)

	out := cmd.OutOrStdout()
	asks := len(names) > 0 && len(picks) > 0 && !utils.ShouldAnswerYes()
	if !schedulerCancelJSON {
		jobs := make([]scheduler.JobInfo, 0, len(picks))
		for _, p := range picks {
			if p.outcome != scheduler.CancelNotFound {
				jobs = append(jobs, p.job)
			}
		}
		scheduler.WriteJobTable(out, jobs, jobTableWidth(out))
	}
	if asks {
		switch {
		case schedulerCancelJSON:
			return writeCancelDoc(cmd, sched, picks, false, nil)
		case !haveTerminal():
			return fmt.Errorf("`condatainer scheduler cancel` asks first when a name picks the jobs, and there is no terminal here; run it in a terminal or pass `-y`")
		case !confirmCancel(cmd.Context(), out, cancelQuestion(len(picks))):
			return nil
		}
	}

	var results []scheduler.CancelResult
	var callErr error
	if len(send) > 0 {
		results, callErr = canceller.CancelJobs(cmd.Context(), send)
	}
	if callErr != nil {
		results = make([]scheduler.CancelResult, len(send))
		for i, id := range send {
			results[i] = scheduler.CancelResult{ID: id, Outcome: scheduler.CancelError, Message: callErr.Error()}
		}
	}
	for i := range picks {
		for _, r := range results {
			if r.ID == picks[i].key {
				picks[i].outcome, picks[i].message = r.Outcome, r.Message
			}
		}
	}

	if schedulerCancelJSON {
		if werr := writeCancelDoc(cmd, sched, picks, true, callErr); werr != nil {
			return werr
		}
	} else {
		printCancelSummary(out, picks)
	}
	if callErr != nil {
		return callErr
	}
	for _, p := range picks {
		if p.outcome != scheduler.CancelSent {
			return errSomeNotCancelled
		}
	}
	return nil
}

// pickToCancel chooses what to cancel from the user's listed jobs, and the IDs to pass to the scheduler.
//   - Named IDs: an ID that is in the listing is sent as the user wrote it, and one that is not is not found
//     and never sent.
//   - Name filters: the jobs whose names match, an array as one job.
func pickToCancel(listed []scheduler.JobInfo, ids, names []string) (picks []cancelPick, send []string) {
	chosen := foldArrays(selectJobs(listed, ids, names), ids, false)
	scheduler.SortJobs(chosen)
	if len(ids) == 0 {
		for _, j := range chosen {
			picks = append(picks, cancelPick{job: j, key: j.ID})
			send = append(send, j.ID)
		}
		return picks, send
	}

	for _, id := range slices.Compact(slices.Sorted(slices.Values(ids))) {
		if array, _, isTask := strings.Cut(id, "_"); isTask && slices.Contains(ids, array) {
			continue // cancelling the array cancels its task
		}
		if !slices.ContainsFunc(listed, func(j scheduler.JobInfo) bool { return j.ID == id || j.ArrayID == id }) {
			picks = append(picks, cancelPick{job: scheduler.JobInfo{ID: id, State: scheduler.JobNotFound}, key: id, outcome: scheduler.CancelNotFound})
			continue
		}
		send = append(send, id)
	}
	for _, j := range chosen {
		key := j.ID
		if !slices.Contains(ids, key) {
			key = j.ArrayID
		}
		picks = append(picks, cancelPick{job: j, key: key})
	}
	slices.SortStableFunc(picks, func(a, b cancelPick) int { return scheduler.CompareJobIDs(a.job.ID, b.job.ID) })
	return picks, send
}

func cancelQuestion(n int) string {
	if n == 1 {
		return "Cancel this job? [y/N]: "
	}
	return fmt.Sprintf("Cancel these %d jobs? [y/N]: ", n)
}

// printCancelSummary reports what the scheduler refused, then the IDs sent and the IDs not found.
func printCancelSummary(w io.Writer, picks []cancelPick) {
	var sent, missing []string
	for _, p := range picks {
		switch p.outcome {
		case scheduler.CancelSent:
			sent = append(sent, p.key)
		case scheduler.CancelNotFound:
			missing = append(missing, p.key)
		case scheduler.CancelError:
			utils.PrintError("%s: %s", p.key, p.message)
		}
	}
	if len(sent) > 0 {
		fmt.Fprintln(w, "Cancelled: "+strings.Join(sent, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintln(w, "Not found: "+strings.Join(missing, ", "))
	}
}

// writeCancelDoc writes the document of a cancel. acted is false for a preview, which has no outcome.
func writeCancelDoc(cmd *cobra.Command, sched scheduler.Scheduler, picks []cancelPick, acted bool, callErr error) error {
	doc := &report.Cancel{Scheduler: jobsSchedulerRecord(sched), Jobs: report.List[report.CancelJob]{}, Problems: report.List[report.Problem]{}}
	if callErr != nil {
		doc.Problems = append(doc.Problems, report.Problem{Message: callErr.Error()})
	}
	for _, p := range picks {
		rec := report.CancelJob{Job: jobRecord(p.job)}
		if acted || p.outcome == scheduler.CancelNotFound {
			rec.Cancel, rec.Error = string(p.outcome), p.message
		}
		doc.Jobs = append(doc.Jobs, rec)
	}
	return report.Write(cmd.OutOrStdout(), doc)
}

// cancelFailure reports a call that could not pick the jobs. With --json the document carries the problem
// and the error is returned after it, so the exit status is not zero.
func cancelFailure(cmd *cobra.Command, sched scheduler.Scheduler, err error) error {
	if !schedulerCancelJSON {
		return err
	}
	doc := &report.Cancel{Jobs: report.List[report.CancelJob]{}, Problems: report.List[report.Problem]{{Message: err.Error()}}}
	if sched != nil {
		doc.Scheduler = jobsSchedulerRecord(sched)
	}
	if werr := report.Write(cmd.OutOrStdout(), doc); werr != nil {
		return werr
	}
	return err
}

func init() {
	schedulerCmd.AddCommand(schedulerCancelCmd)
	jsonFlag(schedulerCancelCmd.Flags(), &schedulerCancelJSON)
}
