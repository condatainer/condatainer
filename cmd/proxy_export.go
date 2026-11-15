package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/runtime/proxy"
	"github.com/spf13/cobra"
)

var proxyExportCmd = &cobra.Command{
	Use:   "export",
	Args:  cobra.NoArgs,
	Short: "Print export statements for the proxy variables",
	Long: `Print export statements for the proxy variables, or nothing if none is active.

- Covers http_proxy, https_proxy, all_proxy, no_proxy and their uppercase forms.
- Load them into the current shell: source <(condatainer proxy export)`,
	Run: func(cmd *cobra.Command, args []string) {
		printProxyExports()
	},
}

// printProxyExports prints shell export statements for all proxy env vars.
// Prints nothing when no proxy is active, so sourcing the output is a no-op.
func printProxyExports() {
	httpURL, ok := proxy.FindActiveProxy()
	if !ok {
		return
	}
	for _, kv := range proxy.ProxyEnvList(httpURL) {
		fmt.Printf("export %s\n", kv)
	}
}
