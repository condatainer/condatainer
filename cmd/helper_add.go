package cmd

import (
	"os"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/spf13/cobra"
)

var helperAddCmd = &cobra.Command{
	Use:   "add [flags] <name>...",
	Short: "Add helpers from the sources",
	Long: `Add helpers from the sources.

- The layer they go in is printed.
- It refuses a name already added, or a file there that add did not install.
- When several sources offer a name, the earliest wins.`,
	Example: `  condatainer helper add code-server
  condatainer helper add code-server rstudio-server -l e
  condatainer helper add code-server --force`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return config.SelectSources(helperSources)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		refuseInsideJob(cmd)
		return helper.Add(cmd.Context(), args, helperLayerFlag(cmd), helperForce, os.Stdout)
	},
}

func init() {
	sourceFlag(helperAddCmd)
	helperAddCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Layer to add to: "+layerChoices+" (default: the furthest writable layer)")
	helperAddCmd.Flags().BoolVar(&helperForce, "force", false, "Replace a file add did not install, or an edited one, and record it")
}
