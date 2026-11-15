package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/condatainer/condatainer/internal/runtime/proxy"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var proxyStopCmd = &cobra.Command{
	Use:          "stop",
	Args:         cobra.NoArgs,
	Short:        "Stop the proxy",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ps, err := proxy.ReadPidFile()
		if err != nil {
			utils.PrintMessage("Proxy is not running (no PID file found)")
			return nil
		}

		currentHost, _ := os.Hostname()
		if ps.Host == currentHost {
			// Same node — kill directly
			proc, err := os.FindProcess(ps.PID)
			if err != nil || proc.Signal(syscall.Signal(0)) == syscall.ESRCH {
				utils.PrintMessage("Proxy already stopped (stale PID file)")
				proxy.RemovePidFile()
				return nil
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed to stop proxy (PID %d): %w", ps.PID, err)
			}
		} else {
			// Different login node — SSH kill
			out, err := exec.Command("ssh", ps.Host,
				"-o", "BatchMode=yes",
				"-o", "ConnectTimeout=5",
				"kill", strconv.Itoa(ps.PID),
			).CombinedOutput()
			if err != nil {
				proxy.RemovePidFile()
				utils.PrintMessage("Proxy already stopped or unreachable: %s", string(out))
				return nil
			}
		}

		utils.PrintSuccess("Proxy stopped")
		return nil
	},
}
