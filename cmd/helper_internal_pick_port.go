package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var pickPortCmd = &cobra.Command{
	Use:   "pick-port",
	Short: "Print a free TCP port on this host",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		port, err := pickFreePort()
		if err != nil {
			return err
		}
		fmt.Println(port)
		return nil
	},
}
