package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var helperRemoveCmd = &cobra.Command{
	Use:   "remove [flags] <name>...",
	Short: "Remove added helpers",
	Long: `Remove helpers that add installed.

- It lists what it will delete and asks first.
- A script you wrote yourself is left in place.
- Saved defaults and run history stay.`,
	Example: `  condatainer helper remove code-server
  condatainer helper remove code-server -l user`,
	Args:              cobra.MinimumNArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: completeHelperScripts,
	RunE:              runHelperRemove,
}

func init() {
	helperRemoveCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Layer to remove from: "+layerChoices+" (default: the nearest added copy)")
}

func runHelperRemove(cmd *cobra.Command, args []string) error {
	refuseInsideJob(cmd)
	items, planErr := helper.PlanRemove(args, helperLayerFlag(cmd))
	if len(items) == 0 {
		return planErr
	}
	if planErr != nil {
		utils.PrintWarning("%v", planErr)
	}
	for _, item := range items {
		fmt.Printf("  remove  %s%s\n", item.Path, config.LayerNote(item.Path))
	}
	if !utils.ShouldAnswerYes() {
		if !utils.IsInteractiveShell() {
			return fmt.Errorf("`condatainer helper remove` asks first, and there is no terminal here; run it in a terminal or pass `-y`")
		}
		if !utils.Confirm(cmd.Context(), os.Stdout, fmt.Sprintf("Remove %d helper(s)? [y/N]: ", len(items))) {
			return nil
		}
	}
	err := helper.Remove(items, os.Stdout)
	if planErr != nil {
		return fmt.Errorf("some helpers were not removed")
	}
	return err
}
