package cmd

import (
	"fmt"
	"strconv"

	"github.com/condatainer/condatainer/internal/runtime/proxy"
	"github.com/spf13/cobra"
)

var proxyDaemonCmd = &cobra.Command{
	Use:    "_proxy_daemon [flags] <ssh-destination> <port>",
	Short:  "Run the proxy's SSH tunnel until it exits (started by `proxy start`)",
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		sshDest := args[0]
		port, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid port: %w", err)
		}
		return proxy.RunDaemon(sshDest, port, proxyDaemonLocalMode, proxyDaemonReportFd)
	},
}

// _proxy_daemon is an internal hidden subcommand invoked by "proxy start" after
// double-forking. It runs the SSH tunnel and blocks until the tunnel exits.
// Without --local: shared mode (0.0.0.0, NFS PID file).
// With --local:    per-job mode (127.0.0.1, node-local PID file).
var proxyDaemonLocalMode bool

var proxyDaemonReportFd int

func init() {
	rootCmd.AddCommand(proxyDaemonCmd)
	proxyDaemonCmd.Flags().BoolVar(&proxyDaemonLocalMode, "local", false, "Per-job mode: bind 127.0.0.1 and write node-local PID file")
	proxyDaemonCmd.Flags().IntVar(&proxyDaemonReportFd, "report-fd", 0, "File descriptor to report startup result to parent")
}
