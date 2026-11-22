package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/server"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Args:  cobra.NoArgs,
	Short: "Show dashboard server status across all login nodes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		currentHost, _ := os.Hostname()
		pidFiles := config.ListServerPidFiles()

		if len(pidFiles) == 0 {
			utils.PrintMessage("Server is not running.")
			return nil
		}

		found := false
		for _, pidFile := range pidFiles {
			ss, err := server.ReadState(pidFile)
			if err != nil {
				continue
			}
			found = true
			if ss.Host == currentHost {
				if server.IsAlive(pidFile) {
					utils.PrintSuccess("Running: %s  port:%d  PID:%d", ss.Host, ss.Port, ss.PID)
					printServerLink(ss.Port)
				} else {
					utils.PrintMessage("Not running: %s (stale PID file)", ss.Host)
				}
			} else {
				utils.PrintSuccess("Running: %s  port:%d  PID:%d", ss.Host, ss.Port, ss.PID)
				printServerLink(ss.Port)
			}
		}
		if !found {
			utils.PrintMessage("Server is not running.")
		}
		return nil
	},
}

// printServerLink prints the dashboard link, with the token, under a running server's line.
func printServerLink(port int) {
	utils.PrintLink(server.TokenLink(fmt.Sprintf("http://localhost:%d/", port)))
}
