package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/runtime/proxy"
	"github.com/spf13/cobra"
)

var proxyURLCmd = &cobra.Command{
	Use:   "url",
	Args:  cobra.NoArgs,
	Short: "Print the proxy URL",
	Long: `Print the active proxy URL (http://host:PORT), or nothing if no proxy is active.

- To load every proxy variable into your shell, use ` + "`condatainer proxy export`" + `.`,
	Run: func(cmd *cobra.Command, args []string) {
		if httpURL, ok := proxy.FindActiveProxy(); ok {
			fmt.Println(httpURL)
		}
	},
}
