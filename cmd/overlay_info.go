package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info [flags] <overlay.img>",
	Short: "Show details about a writable ext3 overlay",
	Long: `Show details about a writable ext3 overlay.

- Filesystem stats, disk and inode usage, block size, ownership.
- For a SquashFS overlay or an installed name, use ` + "`condatainer info`" + `.`,
	Example:      `  condatainer overlay info env.img  # Usage of a writable overlay`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,

	// Enable Smart Tab Completion for .img files
	ValidArgsFunction: completeImages,

	RunE: func(cmd *cobra.Command, args []string) error {
		if !utils.IsImg(args[0]) {
			return fmt.Errorf("%s is not a writable .img overlay; use `condatainer info` for other overlays", args[0])
		}
		return runInfoOverlay(cmd, args)
	},
}
