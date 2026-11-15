package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/image/ext3"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var chownCmd = &cobra.Command{
	Use:   "chown [flags] <overlay.img>",
	Short: "Change file ownership inside an overlay",
	Long:  `Change the UID/GID of files inside an overlay, without mounting it.`,
	Example: `  condatainer overlay chown env.img  # Set entire image to current user
  condatainer overlay chown env.img --root
  condatainer overlay chown env.img -u 1001 -g 1001
  condatainer overlay chown env.img -p /ext3 -p /data`,
	Args: cobra.ExactArgs(1),

	// Enable Smart Tab Completion for .img files
	ValidArgsFunction: completeImages,

	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]

		// Parse Flags
		isRoot, _ := cmd.Flags().GetBool("root")
		targetUID, _ := cmd.Flags().GetInt("uid")
		targetGID, _ := cmd.Flags().GetInt("gid")
		internalPaths, _ := cmd.Flags().GetStringArray("path")

		// Root flag overrides uid/gid
		if isRoot {
			targetUID = 0
			targetGID = 0
			utils.PrintDebug("Mode: Root (0:0)")
		}

		// Feedback to user
		utils.PrintDebug("Chown Targets: %v inside %s", internalPaths, path)
		utils.PrintDebug("New Owner:     UID=%d GID=%d", targetUID, targetGID)

		absPath, _ := filepath.Abs(path)
		lock, err := image.AcquireLock(absPath, true)
		if err != nil {
			ExitWithError("%v", err)
		}
		defer lock.Close()

		// Perform the recursive chown for each path
		for _, internalPath := range internalPaths {
			err = ext3.ChownRecursively(cmd.Context(), path, targetUID, targetGID, internalPath)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(cmd.Context().Err(), context.Canceled) {
					utils.PrintWarning("Operation cancelled.")
					return
				}
				utils.PrintDebug("Failed to chown path %s: %v", internalPath, err)
			}
		}
	},
}

func init() {
	chownCmd.Flags().IntP("uid", "u", os.Getuid(), "User ID to set")
	chownCmd.Flags().IntP("gid", "g", os.Getgid(), "Group ID to set")
	chownCmd.Flags().Bool("root", false, "Set UID and GID to 0 (root); overrides -u and -g")
	chownCmd.Flags().StringArrayP("path", "p", []string{"/"}, "Path inside the overlay (repeatable)")
}
