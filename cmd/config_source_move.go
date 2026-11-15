package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configSourceMoveCmd = &cobra.Command{
	Use:   "move [flags] <name>",
	Short: "Move a recipe source within its layer",
	Long: `Move a recipe source within its config layer. Its URL and token are kept.

- Give exactly one of --first, --last, --before, --after.
- The other source must be in the same layer.
- Moving the default cnt writes it into the layer, then places it.`,
	Example: `  condatainer config source move lab --first
  condatainer config source move lab --before site`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: sourceNameCompletion,
	SilenceUsage:      true,
	RunE:              runSourceMove,
}

func init() {
	placementFlags(configSourceMoveCmd)
	configSourceMoveCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Layer to write: "+layerChoices)
}

func runSourceMove(cmd *cobra.Command, args []string) error {
	name, pos := args[0], sourcePosition()
	if pos == (config.Position{}) {
		return fmt.Errorf("give one of --first, --last, --before, --after")
	}
	path, layer, err := holdingLayer(name)
	if err != nil {
		return err
	}
	list := config.LayerSourceList(path)
	spec, found := catalog.Spec{}, false
	for _, s := range list {
		if s.Name == name {
			spec, found = s, true
		}
	}
	if !found {
		if name != config.DefaultSource().Name {
			return fmt.Errorf("no source named %s in the %s layer", name, utils.StyleName(layer))
		}
		spec = config.DefaultSource()
	}
	placed, err := placeIn(list, spec, pos, layer)
	if err != nil {
		return err
	}
	if err := config.WriteLayerSourceList(path, placed); err != nil {
		return err
	}
	utils.PrintMessage("Moved source %s (%s layer)", name, utils.StyleName(layer))
	return nil
}
