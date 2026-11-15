package cmd

import (
	"context"
	"errors"
	"time"

	ui "github.com/condatainer/condatainer/cmd/internal/ui"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var helperStopCmd = &cobra.Command{
	Use:   "stop [flags] [name]",
	Short: "Stop a running helper",
	Long: `Stop a running helper.

- With no name, it asks which running helper to stop.`,
	Example: `  condatainer helper stop
  condatainer helper stop code-server
  condatainer helper stop --all`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		refuseInsideJob(cmd)
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		return runHelperStop(cmd.Context(), name, helperStopAll)
	},
	ValidArgsFunction: completeHelperScripts,
}

var helperStopAll bool

func init() {
	helperStopCmd.Flags().BoolVarP(&helperStopAll, "all", "a", false, "Stop every match without asking")
}

// runHelperStop stops running instances of a helper. name filters to one helper
// script; empty means all. stopAll stops every match without prompting.
//
//   - name set and one instance running: it is stopped directly.
//   - name empty: the picker is always shown, as confirmation.
//   - several instances and stopAll false: the user chooses one or all via OfferStopPicker.
func runHelperStop(ctx context.Context, name string, stopAll bool) error {
	running, err := helper.RunningHelpers(name)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		if name == "" {
			utils.PrintMessage("No running helpers.")
		} else {
			utils.PrintMessage("No running %s helpers.", name)
		}
		return nil
	}

	var toStop []*helper.HelperRun
	// Skip picker only when a specific name is given and there is exactly one match.
	if stopAll || (name != "" && len(running) == 1) {
		toStop = running
	} else {
		chosen, pickErr := ui.OfferStopPicker(ctx, name, running)
		if pickErr != nil {
			return pickErr
		}
		if len(chosen) == 0 {
			return nil // user cancelled
		}
		toStop = chosen
	}

	for _, r := range toStop {
		if err := stopHelperRun(ctx, r); err != nil {
			utils.PrintWarning("Failed to stop %s: %v", r.ID, err)
		} else {
			utils.PrintSuccess("Stopped %s", r.ID)
		}
	}
	return nil
}

// stopHelperRun stops a single helper run with helper.StopRun and marks its
// history entry "done", even if the signal fails (process already dead). A
// helper on another host is left running and the error returned.
func stopHelperRun(ctx context.Context, r *helper.HelperRun) error {
	if err := helper.StopRun(ctx, r); errors.Is(err, helper.ErrOtherHost) {
		return err
	} else if err != nil {
		utils.PrintDebug("stop %s: %v", r.ID, err)
	}
	return helper.UpdateHistoryStatus(r.ID, "done", time.Now())
}
