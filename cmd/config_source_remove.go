package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configSourceRemoveCmd = &cobra.Command{
	Use:     "remove [flags] <name>",
	Aliases: []string{"rm"},
	Short:   "Remove a recipe source and its token",
	Long: `Remove a recipe source from its config layer, with its recipe token.

- Its registry token goes with it.
- Registry logins stay. ` + "`condatainer registry logout`" + ` removes them.
- Removing cnt from a layer returns it to the default, searched last.`,
	Example:           `  condatainer config source remove lab`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: sourceNameCompletion,
	SilenceUsage:      true,
	RunE:              runSourceRemove,
}

func init() {
	configSourceRemoveCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Layer to write: "+layerChoices)
}

func runSourceRemove(cmd *cobra.Command, args []string) error {
	name := args[0]
	path, layer, err := holdingLayer(name)
	if err != nil {
		return err
	}
	list := config.LayerSourceList(path)
	var kept []catalog.Spec
	var removed *catalog.Spec
	for _, s := range list {
		if s.Name == name {
			removed = &s
			continue
		}
		kept = append(kept, s)
	}
	if removed == nil {
		if name == config.DefaultSource().Name {
			return fmt.Errorf("cnt is the default source and cannot be removed; `--source` leaves it out of one run")
		}
		return fmt.Errorf("no source named %s in the %s layer", name, utils.StyleName(layer))
	}
	if err := config.WriteLayerSourceList(path, kept); err != nil {
		return err
	}
	if file, err := config.CredentialFile(layer); err == nil {
		dropToken(file, credential.TrimScheme(removed.Base))
	}
	utils.PrintMessage("Removed source %s (%s layer)", name, utils.StyleName(layer))
	return nil
}
