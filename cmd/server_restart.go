package cmd

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/server"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var serverRestartCmd = &cobra.Command{
	Use:   "restart",
	Args:  cobra.NoArgs,
	Short: "Restart the dashboard server (stop + start)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if err := requireLoginNode(cmd); err != nil {
			return err
		}

		pidFile := config.GetServerPidFilePath()
		ss, err := server.ReadState(pidFile)

		// Keep the running server's daemon mode unless --daemon was given.
		daemon := serverRestartDaemon
		if !cmd.Flags().Changed("daemon") && ss != nil {
			daemon = ss.Daemon
		}

		// Determine target port: flag > saved state > saved config > free port.
		port := serverRestartPort
		if port != 0 {
			if err := config.SaveServerPort(port); err != nil {
				utils.PrintDebug("saving server port: %v", err)
			}
		} else if ss != nil {
			port = ss.Port
		} else {
			port = config.ReadSavedServerPort()
		}

		if err != nil || ss == nil {
			utils.PrintMessage("Server is not running, starting fresh.")
			return startServerDaemon(port, daemon)
		}

		p, err := os.FindProcess(ss.PID)
		if err != nil || p.Signal(syscall.Signal(0)) != nil {
			utils.PrintMessage("Server process not found, starting fresh.")
			return startServerDaemon(port, daemon)
		}
		if err := p.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("sending SIGTERM to PID %d: %w", ss.PID, err)
		}
		utils.PrintMessage("Stopping server (PID %d)…", ss.PID)

		// Wait for the process to exit (up to 10s).
		for range 50 {
			time.Sleep(200 * time.Millisecond)
			if p.Signal(syscall.Signal(0)) != nil {
				break
			}
		}

		utils.PrintMessage("Starting server on port %d…", port)
		return startServerDaemon(port, daemon)
	},
}

var (
	serverRestartPort   int
	serverRestartDaemon bool
)

func init() {
	serverRestartCmd.Flags().IntVarP(&serverRestartPort, "port", "p", 0, "Restart on a different port (remembered for the next start)")
	serverRestartCmd.Flags().BoolVar(&serverRestartDaemon, "daemon", false, "Keep running after SSH logout (default: keep current mode)")
}
