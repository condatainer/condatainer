package cmd

import (
	"github.com/condatainer/condatainer/internal/image/ext3"
	"github.com/spf13/cobra"
)

var fscheckCmd = &cobra.Command{
	Use:     "fscheck [flags] <overlay.img>",
	Aliases: []string{"fsck"},
	Short:   "Verify filesystem integrity (e2fsck)",
	Example: `  condatainer overlay fscheck env.img     # Check and repair automatically
  condatainer overlay fscheck env.img -f`,
	Args: cobra.ExactArgs(1),

	// Enable Smart Tab Completion for .img files
	ValidArgsFunction: completeImages,

	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]
		force, _ := cmd.Flags().GetBool("force")

		if err := ext3.CheckIntegrity(cmd.Context(), path, force); err != nil {
			ExitWithError("%v", err)
		}
	},
}

func init() {
	fscheckCmd.Flags().BoolP("force", "f", false, "Force check even if filesystem appears clean")
}
