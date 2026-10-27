package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(mountSentinelCmd)
	rootCmd.AddCommand(mountExecCmd)
}

var mountSentinelCmd = &cobra.Command{
	Use:    "_mount_sentinel <fuseBin> <mnt> -- [fuseArgs...]",
	Hidden: true,
	Short:  "Run the FUSE-mount sentinel condatainer re-execs itself into (internal)",
	Args:   cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		fuseBin, mnt := args[0], args[1]
		fuseArgs := args[2:]
		return freeze.RunSentinel(fuseBin, mnt, os.Getenv(freeze.EnvSentinelWork), fuseArgs)
	},
}

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
