package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var schedulerInfoCmd = &cobra.Command{
	SilenceUsage: true, // Runtime errors should not show usage
	Use:          "info",
	Args:         cobra.NoArgs,
	Short:        "Show the detected job scheduler",
	Long: `Show the detected job scheduler: type, binary path, version, and availability.

Inside a scheduled job it reports the scheduler as unavailable.`,
	Example: `  condatainer scheduler info
  condatainer scheduler info --host`,
	RunE: runSchedulerInfo,
}

func init() {
	jsonFlag(schedulerInfoCmd.Flags(), &schedulerInfoJSON)
	addHostFlags(schedulerInfoCmd.Flags())
}

func runSchedulerInfo(cmd *cobra.Command, args []string) error {
	if done, err := runOnHostIfAsked(cmd, args); done {
		return err
	}
	sched, insideJob, err := lookupScheduler()

	if schedulerInfoJSON {
		doc := &report.SchedulerInfo{Problems: report.List[report.Problem]{}}
		if err != nil {
			doc.Problems = append(doc.Problems, report.Problem{Message: err.Error()})
		} else {
			doc.Scheduler = schedulerRecord(cmd.Context(), sched, insideJob)
		}
		return report.Write(cmd.OutOrStdout(), doc)
	}

	if insideJob {
		printInsideJob()
		return nil
	}
	if err != nil {
		printNoScheduler()
		return nil
	}

	// Structured output has no [CNT] prefix.
	fmt.Println("Scheduler Information:")
	fmt.Printf("  Type:      %s\n", string(sched.GetType()))
	fmt.Printf("  Binary:    %s\n", sched.GetBinary())
	if version := sched.GetVersion(cmd.Context()); version != "" {
		fmt.Printf("  Version:   %s\n", version)
	}

	switch {
	case sched.IsInsideJob():
		fmt.Printf("  Status:    %s (inside job)\n", utils.StyleError("Unavailable"))
		fmt.Println()
		fmt.Println("You are currently inside a scheduled job (detected via environment).")
		fmt.Println("Job submission is disabled to prevent nested job submissions.")
	case sched.IsAvailable():
		fmt.Printf("  Status:    %s\n", utils.StyleSuccess("Available"))
		fmt.Println()
		fmt.Println("The scheduler is available and ready for job submission.")
	default:
		fmt.Printf("  Status:    %s\n", utils.StyleError("Unavailable"))
		fmt.Println()
		fmt.Println("Scheduler detected but not available for job submission.")
	}
	return nil
}
