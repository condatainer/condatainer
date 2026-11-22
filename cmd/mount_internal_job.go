package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/spf13/cobra"
)

var mountJobCmd = &cobra.Command{
	Use:    "_mount_job <spec>",
	Hidden: true,
	Short:  "Run the apptainer job a FUSE mount runs in (internal)",
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		err := freeze.RunJob(ctx, args[0])
		if code, ok := freeze.JobExitCode(err); ok {
			os.Exit(code)
		}
		return err
	},
}

func init() {
	rootCmd.AddCommand(mountJobCmd)
}
