package cmd

import (
	"github.com/spf13/cobra"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage proxy tunnels for compute nodes",
	Long: `Manage SSH proxy tunnels so compute-node jobs can reach the internet.

Shared mode (login node):
  - Run ` + "`condatainer proxy start`" + ` once on the login node.
  - condatainer commands inside jobs then use the tunnel automatically.

Per-job mode (compute node):
  - Run ` + "`condatainer proxy start --via <login-node>`" + ` inside a job.
  - Or start one for each job automatically:
      condatainer config append scheduler.proxy_per_job all
    Use create, helper or run instead of all to limit it to those jobs.`,
}

func init() {
	rootCmd.AddCommand(proxyCmd)
	proxyCmd.AddCommand(proxyStartCmd, proxyStopCmd, proxyStatusCmd, proxyURLCmd, proxyExportCmd)
}
