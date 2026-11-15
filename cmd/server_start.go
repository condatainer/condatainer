package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/server"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Args:  cobra.NoArgs,
	Short: "Start the dashboard server in the background",
	Long: `Start the dashboard server in the background.

- By default the server exits when your SSH session ends.
- --daemon keeps it running.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if err := requireLoginNode(cmd); err != nil {
			return err
		}

		port := serverStartPort
		if port != 0 {
			// Explicit --port: persist it for future starts.
			if err := config.SaveServerPort(port); err != nil {
				utils.PrintDebug("saving server port: %v", err)
			}
		} else {
			port = config.ReadSavedServerPort()
			if port == 0 {
				var err error
				port, err = pickFreePort()
				if err != nil {
					return fmt.Errorf("finding free port: %w", err)
				}
			}
		}

		pidFile := config.GetServerPidFilePath()
		if server.IsAlive(pidFile) {
			if ss, err := server.ReadState(pidFile); err == nil {
				utils.PrintMessage("Server already running at http://localhost:%d/", ss.Port)
				utils.PrintMessage("Log: %s", config.GetServerLogPath())
				return nil
			}
		}

		return startServerDaemon(port, serverStartDaemon)
	},
}

var (
	serverStartPort   int
	serverStartDaemon bool
)

func init() {
	serverStartCmd.Flags().IntVarP(&serverStartPort, "port", "p", 0, "Port to listen on (remembered for the next start)")
	serverStartCmd.Flags().BoolVar(&serverStartDaemon, "daemon", false, "Keep running after SSH logout")
}
