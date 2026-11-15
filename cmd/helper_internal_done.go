package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/spf13/cobra"
)

var serverDoneCmd = &cobra.Command{
	Use:    "done",
	Hidden: true,
	Short:  "Signal that a helper job has finished (called from the wrapper script)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		id := serverDoneID
		if id == "" {
			id = os.Getenv("CNT_HELPER_ID")
		}
		if id == "" {
			return fmt.Errorf("--id or $CNT_HELPER_ID is required")
		}
		return helper.AppendEvent(id, helper.StateEvent{
			Type:     "done",
			ExitCode: &serverDoneExitCode,
		})
	},
}

var (
	serverDoneID       string
	serverDoneExitCode int
)

func init() {
	serverDoneCmd.Flags().StringVar(&serverDoneID, "id", "", "Helper run ID (defaults to $CNT_HELPER_ID)")
	serverDoneCmd.Flags().IntVar(&serverDoneExitCode, "exit-code", 0, "Process exit code")
}
