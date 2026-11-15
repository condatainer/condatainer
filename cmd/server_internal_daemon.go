package cmd

import (
	"os"

	"github.com/condatainer/condatainer/internal/server"
	"github.com/spf13/cobra"
)

var serverDaemonCmd = &cobra.Command{
	Use:    "_server_daemon",
	Hidden: true,
	Short:  "Run the condatainer server daemon (started by `server start`)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		port := serverDaemonPort
		if port == 0 {
			var err error
			port, err = pickFreePort()
			if err != nil {
				return err
			}
		}

		var reportPipe *os.File
		if serverDaemonReportFd > 0 {
			reportPipe = os.NewFile(uintptr(serverDaemonReportFd), "report")
		}

		return server.RunDaemon(port, reportPipe, serverDaemonWatchPID)
	},
}

var (
	serverDaemonPort     int
	serverDaemonReportFd int
	serverDaemonWatchPID int
)

func init() {
	rootCmd.AddCommand(serverDaemonCmd)
	serverDaemonCmd.Flags().IntVar(&serverDaemonPort, "port", 0, "Port to listen on (0 = use saved or free port)")
	serverDaemonCmd.Flags().IntVar(&serverDaemonReportFd, "report-fd", 0, "File descriptor to signal readiness to parent")
	serverDaemonCmd.Flags().IntVar(&serverDaemonWatchPID, "watch-pid", 0, "Exit when this PID dies (SSH session tracking)")
}
