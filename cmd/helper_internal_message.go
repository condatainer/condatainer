package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/spf13/cobra"
)

var serverMessageCmd = &cobra.Command{
	Use:   "message [flags] <text>",
	Short: "Append a message to the helper messages file (called from compute node)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		id := serverMessageID
		if id == "" {
			id = os.Getenv("CNT_HELPER_ID")
		}
		if id == "" {
			return fmt.Errorf("--id or $CNT_HELPER_ID is required")
		}
		level := serverMessageLevel
		if level == "" {
			level = "info"
		}
		return helper.AppendEvent(id, helper.StateEvent{Type: "msg", Level: level, Text: args[0]})
	},
}

var (
	serverMessageID    string
	serverMessageLevel string
)

func init() {
	serverMessageCmd.Flags().StringVar(&serverMessageID, "id", "", "Helper run ID (defaults to $CNT_HELPER_ID)")
	serverMessageCmd.Flags().StringVar(&serverMessageLevel, "level", "info", "Message level: info|warn|error")
}
