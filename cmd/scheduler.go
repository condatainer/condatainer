package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/runtime/hostrun"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	schedulerInfoJSON       bool
	schedulerPartitionsJSON bool
	schedulerHost           bool
	schedulerHostOn         string
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

var errHostOnNeedsHost = errors.New("--host-on needs --host from inside a container, where it picks the node the command runs on")

func init() {
	rootCmd.AddCommand(schedulerCmd)
	schedulerCmd.AddCommand(schedulerInfoCmd, schedulerPartitionsCmd, schedulerJobsCmd, schedulerCancelCmd)
}

// addHostFlags adds --host and --host-on to a member of the group.
func addHostFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&schedulerHost, "host", false, "Run on the host through host-run (from inside a container)")
	fs.StringVar(&schedulerHostOn, "host-on", "", "Node to run on: localhost, login or a node name (default: host_run.on)")
}

// runOnHostIfAsked sends the command to the host when --host is given inside a container. done reports that
// the host ran it, so the caller returns.
//   - The host runs the same command with the same flags, except --host and --host-on.
//   - --host on the host changes nothing. --host-on without --host inside a container is refused.
func runOnHostIfAsked(cmd *cobra.Command, args []string) (done bool, err error) {
	switch {
	case schedulerHost && runInsideContainer():
		argv := append([]string{"condatainer", "scheduler", cmd.Name()}, flagArgv(cmd.Flags(), map[string]bool{"host": true, "host-on": true})...)
		if len(args) > 0 {
			argv = append(append(argv, "--"), args...)
		}
		return true, runOnHost(cmd.Context(), argv, schedulerHostOn, "--host")
	case schedulerHostOn != "":
		return true, errHostOnNeedsHost
	}
	return false, nil
}

// hasHostRelay reports whether this is a container with a relay to the host, so --host can work.
func hasHostRelay() bool {
	return runInsideContainer() && os.Getenv(hostrun.EnvSock) != ""
}

// noScheduler is errNoScheduler. A container with a relay to the host is told to add --host.
func noScheduler() error {
	if hasHostRelay() {
		return fmt.Errorf("%w: add --host to ask the host", errNoScheduler)
	}
	return errNoScheduler
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
		return nil, false, noScheduler()
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
	if hasHostRelay() {
		utils.PrintMessage("Add --host to ask the host.")
	}
}
