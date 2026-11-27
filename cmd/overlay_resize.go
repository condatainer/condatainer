package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/image/ext3"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/condatainer/condatainer/internal/utils/units"
	"github.com/spf13/cobra"
)

var resizeCmd = &cobra.Command{
	Use:   "resize [flags] <overlay.img>",
	Short: "Expand or shrink an overlay",
	Example: `  condatainer overlay resize env.img -s 20g
  condatainer overlay resize data.img --size 512M
  condatainer overlay resize env.img -s 20g --sparse`,
	Args: cobra.ExactArgs(1),

	// Enable Smart Tab Completion for .img files
	ValidArgsFunction: completeImages,

	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]

		// 1. Get Size Flag
		sizeStr, _ := cmd.Flags().GetString("size")
		if sizeStr == "" {
			utils.PrintError("Size flag is required (e.g., -s 20G)")
			_ = cmd.Usage()
			os.Exit(ExitCodeError)
		}

		// 2. Parse Size
		sizeMB64, err := units.ParseSizeInput(sizeStr)
		if err == nil && sizeMB64 == 0 {
			err = fmt.Errorf("a size is required")
		}
		if err != nil {
			ExitWithError("%v", err)
		}
		sizeMB := int(sizeMB64)

		sparse, _ := cmd.Flags().GetBool("sparse")

		// 3. Execute — Resize takes the exclusive lock itself, so none is held here.
		if err := ext3.Resize(cmd.Context(), path, sizeMB, sparse); err != nil {
			ExitWithError("%v", err)
		}
	},
}

func init() {
	resizeCmd.Flags().StringP("size", "s", "", "New size; a bare number is GiB (e.g. 20, 512M)")
	resizeCmd.Flags().BoolP("sparse", "S", false, "Leave the overlay sparse (no pre-allocation)")
	_ = resizeCmd.MarkFlagRequired("size")
}
