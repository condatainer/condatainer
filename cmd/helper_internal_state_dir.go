package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/spf13/cobra"
)

var helperStateDirCmd = &cobra.Command{
	Use:    "state-dir <id>",
	Hidden: true,
	Short:  "Print the state directory of a helper run ID",
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		dir := helper.StateDir(args[0])
		if dir == "" {
			return fmt.Errorf("cannot determine state directory")
		}
		fmt.Println(dir)
		return nil
	},
}
