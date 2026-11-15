package cmd

import (
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configPrependCmd = &cobra.Command{
	Use:   "prepend [flags] <key> <value>",
	Short: "Prepend a value to an array config key (highest priority)",
	Long: `Prepend a value to an array config key (highest search priority).

` + configLayersHelp,
	Example:           `  condatainer config prepend channels bioconda`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: arrayKeyCompletion,
	SilenceUsage:      true,
	Run: func(cmd *cobra.Command, args []string) {
		key, value := args[0], args[1]
		if strings.TrimSpace(value) == "" {
			ExitWithError("value cannot be empty")
		}
		value = parseArrayValue(key, value)
		moved := false
		layer, err := modifyArrayConfig(key, func(cur []string) []string {
			out := make([]string, 0, len(cur))
			for _, v := range cur {
				if v == value {
					moved = true
					continue
				}
				out = append(out, v)
			}
			return append([]string{value}, out...)
		})
		if err != nil {
			ExitWithError("%v", err)
		}
		if moved {
			utils.PrintMessage("Moved %s to the front of %s (%s layer)", value, key, utils.StyleName(layer))
		} else {
			utils.PrintMessage("Prepended %s to %s (%s layer)", value, key, utils.StyleName(layer))
		}
	},
}

func init() {
	configPrependCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Layer to write: "+layerChoices)
}
