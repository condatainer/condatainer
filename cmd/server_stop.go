package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/server"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var serverStopCmd = &cobra.Command{
	Use:   "stop [flags] [node]",
	Short: "Stop the dashboard server",
	Long: `Stop the dashboard server.

- Without arguments, it stops the server on the current login node.
- A node name (or part of one) stops the server on another login node.`,
	Example: `  condatainer server stop         # Stop the local server
  condatainer server stop login2  # Stop the server on the node matching login2`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if err := requireLoginNode(cmd); err != nil {
			return err
		}

		nodePattern := serverStopNodeFlag
		if nodePattern == "" && len(args) > 0 {
			nodePattern = args[0]
		}

		currentHost, _ := os.Hostname()

		// No pattern or pattern matches local host → stop local server.
		if nodePattern == "" || matchHost(nodePattern, currentHost) {
			pidFile := config.GetServerPidFilePath()
			ss, err := server.ReadState(pidFile)
			if err != nil {
				utils.PrintMessage("Server is not running.")
				return nil
			}
			p, err := os.FindProcess(ss.PID)
			if err != nil || p.Signal(syscall.Signal(0)) != nil {
				utils.PrintMessage("Server is not running (stale PID file).")
				os.Remove(pidFile)
				return nil
			}
			if err := p.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("sending SIGTERM to PID %d: %w", ss.PID, err)
			}
			utils.PrintSuccess("Server (PID %d) stopped.", ss.PID)
			return nil
		}

		// Pattern given → scan all PID files and stop on matching remote host.
		for _, pidFile := range config.ListServerPidFiles() {
			ss, err := server.ReadState(pidFile)
			if err != nil {
				continue
			}
			if !matchHost(nodePattern, ss.Host) {
				continue
			}
			if err := stopRemoteServer(ss); err != nil {
				return fmt.Errorf("stopping server on %s: %w", ss.Host, err)
			}
			utils.PrintSuccess("Server on %s (PID %d) stopped.", ss.Host, ss.PID)
			return nil
		}

		utils.PrintMessage("No server found matching %q.", nodePattern)
		return nil
	},
}

var serverStopNodeFlag string

func init() {
	serverStopCmd.Flags().StringVarP(&serverStopNodeFlag, "node", "n", "", "Login node to stop (partial match, e.g. login2)")
}

// matchHost reports whether pattern partially matches hostname.
// Exact match, or hostname starts with pattern+"." (subdomain prefix), or contains.
func matchHost(pattern, hostname string) bool {
	if hostname == pattern {
		return true
	}
	if strings.HasPrefix(hostname, pattern+".") {
		return true
	}
	return strings.Contains(hostname, pattern)
}

// stopRemoteServer sends SIGTERM to a server on a remote login node via SSH.
func stopRemoteServer(ss *config.ServerState) error {
	return exec.Command("ssh", ss.Host, "kill", "-TERM", strconv.Itoa(ss.PID)).Run()
}
