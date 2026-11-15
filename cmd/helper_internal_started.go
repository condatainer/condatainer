package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/spf13/cobra"
)

var serverStartedCmd = &cobra.Command{
	Use:    "started",
	Hidden: true,
	Short:  "Record the node a helper job started on (called from the wrapper script)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		id := serverStartedID
		if id == "" {
			id = os.Getenv("CNT_HELPER_ID")
		}
		if id == "" {
			return fmt.Errorf("--id or $CNT_HELPER_ID is required")
		}
		node := serverStartedNode
		if node == "" {
			node, _ = os.Hostname()
		}
		return helper.AppendEvent(id, helper.StateEvent{
			Type:  "start",
			Node:  node,
			JobID: os.Getenv("CNT_HELPER_JOB_ID"),
		})
	},
}

var (
	serverStartedID   string
	serverStartedNode string
)

func init() {
	serverStartedCmd.Flags().StringVar(&serverStartedID, "id", "", "Helper run ID (defaults to $CNT_HELPER_ID)")
	serverStartedCmd.Flags().StringVar(&serverStartedNode, "node", "", "Node name (defaults to the host name)")
}
