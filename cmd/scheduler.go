package cmd

import (
	"context"
	"errors"
	"strings"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	schedulerInfoJSON       bool
	schedulerPartitionsJSON bool
)

var schedulerCmd = &cobra.Command{
	Use:   "scheduler",
	Short: "Show the job scheduler, its partitions and your jobs",
	Long: `Show the job scheduler of this system, and your jobs on it.

- info shows the scheduler and whether it takes jobs.
- partitions lists the partitions/queues you can pass to --partition.
- jobs lists your jobs, or shows one job in full.
- cancel cancels your jobs.`,
}

var errNoScheduler = errors.New("no job scheduler detected on this system")

func init() {
	rootCmd.AddCommand(schedulerCmd)
	schedulerCmd.AddCommand(schedulerInfoCmd, schedulerPartitionsCmd, schedulerJobsCmd, schedulerCancelCmd)
}

// lookupScheduler finds the scheduler to report on. Inside a scheduled job it looks for none and
// reports insideJob, so no binary is probed there.
func lookupScheduler() (sched scheduler.Scheduler, insideJob bool, err error) {
	if scheduler.IsInsideJob() {
		return nil, true, nil
	}
	// Use the already-initialized scheduler if there is one.
	if sched := scheduler.ActiveScheduler(); sched != nil {
		return sched, false, nil
	}
	sched, err = scheduler.DetectSchedulerWithBinary(scheduler.Bin())
	if err != nil {
		return nil, false, errNoScheduler
	}
	return sched, false, nil
}

// schedulerRecord describes the scheduler for the JSON documents.
func schedulerRecord(ctx context.Context, sched scheduler.Scheduler, insideJob bool) *report.Scheduler {
	if sched == nil {
		return &report.Scheduler{InsideJob: insideJob}
	}
	inside := insideJob || sched.IsInsideJob()
	return &report.Scheduler{
		Type:      strings.ToLower(string(sched.GetType())),
		Binary:    sched.GetBinary(),
		Version:   sched.GetVersion(ctx),
		Available: sched.IsAvailable() && !inside,
		InsideJob: inside,
	}
}

func printInsideJob() {
	utils.PrintMessage("Scheduler Status: %s", utils.StyleWarning("Unavailable (inside job)"))
	utils.PrintMessage("")
	utils.PrintMessage("You are currently inside a scheduled job; job submission is disabled to prevent nested submissions.")
}

func printNoScheduler() {
	utils.PrintMessage("Scheduler Status: %s", utils.StyleError("Not Found"))
}
