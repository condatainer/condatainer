package cmd

import (
	"os"

	"github.com/condatainer/condatainer/internal/runtime/proxy"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var proxyStatusCmd = &cobra.Command{
	Use:   "status",
	Args:  cobra.NoArgs,
	Short: "Show proxy status",
	Run: func(cmd *cobra.Command, args []string) {
		// Per-job local proxy
		if ps, err := proxy.ReadLocalPidFile(); err == nil {
			if proxy.ProxyAlive(ps.Host, ps.Port) {
				if ps.Via != ps.Host {
					utils.PrintSuccess("Per-job proxy running on %s:%d (PID %d, via %s)", ps.Host, ps.Port, ps.PID, ps.Via)
				} else {
					utils.PrintSuccess("Per-job proxy running on %s:%d (PID %d)", ps.Host, ps.Port, ps.PID)
				}
				utils.PrintMessage("This job uses: http://%s:%d", ps.Host, ps.Port)
				return
			}
			utils.PrintWarning("Per-job proxy dead — stale PID file at %s", proxy.LocalPidFilePath())
			os.Remove(proxy.LocalPidFilePath()) //nolint:errcheck
		}

		// Shared NFS proxy
		ps, err := proxy.ReadPidFile()
		if err != nil {
			utils.PrintMessage("Proxy not running")
			return
		}
		if proxy.ProxyAlive(ps.Host, ps.Port) {
			if ps.Via != ps.Host {
				utils.PrintSuccess("Shared proxy running on %s:%d (PID %d, via %s)", ps.Host, ps.Port, ps.PID, ps.Via)
			} else {
				utils.PrintSuccess("Shared proxy running on %s:%d (PID %d)", ps.Host, ps.Port, ps.PID)
			}
			utils.PrintMessage("Jobs will use: http://%s:%d", ps.Host, ps.Port)
		} else {
			utils.PrintWarning("Proxy dead — stale PID file at %s", proxy.PidFilePath())
			proxy.RemovePidFile()
			utils.PrintMessage("Run `condatainer proxy start` to start a new one.")
		}
	},
}
