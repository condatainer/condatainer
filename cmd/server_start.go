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
- --daemon keeps it running.
- --rotate-token replaces the dashboard token.
- The token is in ~/.local/state/condatainer/server.token.
- You may write your own token there.
- It needs 16 or more of A-Z a-z 0-9 . _ ~ - and mode 600.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if err := requireLoginNode(cmd); err != nil {
			return err
		}

		if serverStartRotateToken {
			if err := rotateServerToken(); err != nil {
				return err
			}
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
				utils.PrintMessage("Server already running at:")
				utils.PrintLink(server.TokenLink(fmt.Sprintf("http://localhost:%d/", ss.Port)))
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

	serverStartRotateToken bool
)

func init() {
	serverStartCmd.Flags().IntVarP(&serverStartPort, "port", "p", 0, "Port to listen on (remembered for the next start)")
	serverStartCmd.Flags().BoolVar(&serverStartDaemon, "daemon", false, "Keep running after SSH logout")
	serverStartCmd.Flags().BoolVar(&serverStartRotateToken, "rotate-token", false, "Replace the dashboard token")
}
