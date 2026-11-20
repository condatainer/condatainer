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

Inside a scheduled job the status says that job submission is disabled.
Listing jobs and partitions still works there.`,
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
	sched, err := lookupQueryScheduler()

	if schedulerInfoJSON {
		doc := &report.SchedulerInfo{Problems: report.List[report.Problem]{}}
		if err != nil {
			doc.Problems = append(doc.Problems, report.Problem{Message: err.Error()})
			doc.Scheduler = schedulerRecord(nil, "")
		} else {
			doc.Scheduler = schedulerRecord(sched, sched.GetVersion(cmd.Context()))
		}
		return report.Write(cmd.OutOrStdout(), doc)
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

	switch schedulerStatus(sched) {
	case report.SchedulerInsideJob:
		fmt.Printf("  Status:    %s\n", utils.StyleWarning("Submission disabled (inside job)"))
		fmt.Println()
		fmt.Println("You are inside a scheduled job (detected via environment).")
		fmt.Println("Job submission is disabled to prevent nested job submissions.")
		fmt.Println("Listing jobs and partitions still works.")
	default:
		fmt.Printf("  Status:    %s\n", utils.StyleSuccess("Available"))
		fmt.Println()
		fmt.Println("The scheduler is available and ready for job submission.")
	}
	return nil
}
