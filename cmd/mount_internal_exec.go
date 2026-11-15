package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/spf13/cobra"
)

var mountExecCmd = &cobra.Command{
	Use:                "_mount_exec -- <bin> [args...]",
	Hidden:             true,
	Short:              "Run a FUSE server without the capabilities that skip file permissions (internal)",
	DisableFlagParsing: true,
	Args:               cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			return fmt.Errorf("_mount_exec needs a program to run")
		}
		return freeze.ExecWithoutDACOverride(args[0], args[1:])
	},
}

func init() {
	rootCmd.AddCommand(mountExecCmd)
}
