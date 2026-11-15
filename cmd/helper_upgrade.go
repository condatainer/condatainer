package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var helperUpgradeCmd = &cobra.Command{
	Use:   "upgrade [flags] [name...]",
	Short: "Bring added helpers up to date",
	Long: `Bring added helpers up to date.

- It prints what it will replace, layer by layer, and asks before it does.
- Without a terminal and without -y, it prints the plan and changes nothing.
- Kept and reported: a helper you edited, and one no source offers any more.
- Also kept and reported: a helper in a layer you cannot write.`,
	Example: `  condatainer helper upgrade
  condatainer helper upgrade code-server
  condatainer helper upgrade -l extra-root -y`,
	SilenceUsage:      true,
	ValidArgsFunction: completeHelperScripts,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return config.SelectSources(helperSources)
	},
	RunE: runHelperUpgrade,
}

func init() {
	sourceFlag(helperUpgradeCmd)
	helperUpgradeCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Limit to a layer: "+layerChoices+" (default: every layer)")
}

func runHelperUpgrade(cmd *cobra.Command, args []string) error {
	refuseInsideJob(cmd)
	ctx := cmd.Context()
	plan, err := helper.PlanUpgrade(ctx, args, helperLayerFlag(cmd), os.Stdout)
	if err != nil {
		return err
	}
	replace := printUpgradePlan(plan)
	if replace == 0 {
		return nil
	}
	if !utils.ShouldAnswerYes() {
		if !utils.IsInteractiveShell() {
			return fmt.Errorf("`condatainer helper upgrade` asks first, and there is no terminal here; run it in a terminal or pass `-y`")
		}
		if !utils.ConfirmDefaultYes(ctx, os.Stdout, fmt.Sprintf("Replace %d helper(s)? [Y/n] ", replace)) {
			return nil
		}
	}
	return helper.ApplyUpgrade(ctx, plan, os.Stdout)
}

// printUpgradePlan prints what an upgrade will do layer by layer and returns how many helpers it replaces.
//   - A helper that is already current is counted, not listed.
func printUpgradePlan(plan []helper.UpgradeItem) int {
	replace, current, kept := 0, 0, 0
	lastDir := ""
	for _, item := range plan {
		if item.Action == helper.UpgradeCurrent {
			current++
			continue
		}
		if item.Dir != lastDir {
			lastDir = item.Dir
			fmt.Printf("%s (%s)\n", item.Layer, item.Dir)
		}
		switch item.Action {
		case helper.UpgradeReplace:
			replace++
			fmt.Printf("  replace  %s\n", item.Name)
		case helper.UpgradeEdited:
			kept++
			fmt.Printf("  keep     %s: edited since it was added; `condatainer helper add --force %s` replaces it\n", item.Name, item.Name)
		case helper.UpgradeDropped:
			kept++
			fmt.Printf("  keep     %s: no source offers it any more\n", item.Name)
		case helper.UpgradeUnwritable:
			kept++
			fmt.Printf("  keep     %s: you cannot write this layer; whoever can runs `condatainer helper upgrade -l %s`\n", item.Name, item.Layer)
		}
	}
	switch {
	case replace == 0 && kept == 0 && current > 0:
		utils.PrintMessage("Everything added is up to date.")
	case replace == 0 && len(plan) == 0:
		utils.PrintMessage("No helpers added. See `condatainer helper avail`.")
	}
	return replace
}
