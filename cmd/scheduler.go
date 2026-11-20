package cmd

import (
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

// lookupQueryScheduler finds the scheduler for a command that only reads. It also works inside a scheduled
// job, since reading state submits nothing.
func lookupQueryScheduler() (scheduler.Scheduler, error) {
	if sched := scheduler.ActiveScheduler(); sched != nil {
		return sched, nil
	}
	sched, err := scheduler.DetectSchedulerWithBinary(scheduler.Bin())
	if err != nil {
		return nil, noScheduler()
	}
	return sched, nil
}

// schedulerStatus is what can be done with a scheduler that was found. Inside a scheduled job submission is off.
func schedulerStatus(sched scheduler.Scheduler) report.SchedulerStatus {
	if scheduler.IsInsideJob() || sched.IsInsideJob() {
		return report.SchedulerInsideJob
	}
	return report.SchedulerAvailable
}

// schedulerRecord describes the scheduler for the JSON documents. A nil sched is no scheduler found.
// The version is a call of its own, so a document that does not read it passes "".
func schedulerRecord(sched scheduler.Scheduler, version string) *report.Scheduler {
	if sched == nil {
		return &report.Scheduler{Status: report.SchedulerNotFound}
	}
	return &report.Scheduler{
		Type:    strings.ToLower(string(sched.GetType())),
		Binary:  sched.GetBinary(),
		Version: version,
		Status:  schedulerStatus(sched),
	}
}

func printNoScheduler() {
	utils.PrintMessage("Scheduler Status: %s", utils.StyleError("Not Found"))
	if hasHostRelay() {
		utils.PrintMessage("Add --host to ask the host.")
	}
}
