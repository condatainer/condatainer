package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configGetCmd = &cobra.Command{
	Use:   "get [flags] <key>",
	Short: "Get a configuration value",
	Long: `Get a configuration value.

- Without -l, it returns the effective value (environment and every layer).

` + configLayersHelp,
	Example: `  condatainer config get host_apptainer
  condatainer config get build.ncpu
  condatainer config get sources
  condatainer config get sources -l cnt-root  # Only that layer's file`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: configKeysCompletion,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		if key == "sources" {
			printSourcesKey()
			return
		}

		// Layer-specific read
		if getLayer != "" {
			configPath, _, err := config.ResolveReadableConfigPath(getLayer)
			if err != nil {
				ExitWithError("Invalid layer: %v", err)
			}
			if isArrayKey(key) {
				for _, v := range config.ReadConfigSliceKey(configPath, key) {
					fmt.Println(v)
				}
			} else {
				fmt.Println(config.ReadConfigKey(configPath, key))
			}
			return
		}

		// Merged effective value: env > layers in priority order > default
		if res, ok := settings.Resolve(key); ok {
			printEffective(res)
			return
		}
		if k, a, ok := settings.LookupAlias(key); ok {
			utils.PrintNote("%s", a.RenameMessage())
			res, _ := settings.Resolve(k.Name)
			printEffective(res)
			return
		}
		if r, ok := settings.LookupRemoved(key); ok {
			ExitWithError("%s", r.Text())
		}
		ExitWithError("Unknown config key: %s", key)
	},
}

func init() {
	configGetCmd.Flags().StringVarP(&getLayer, "layer", "l", "", "Read only this layer: "+layerChoices)
}

// configKeysCompletion returns config keys for shell completion
func configKeysCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		// First arg: complete all config keys
		var keys []string
		for k := range knownConfigKeys() {
			keys = append(keys, k)
		}
		return keys, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		// Second arg: complete values based on the key
		return configValueCompletion(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// printEffective prints the effective value of a registered key, one line per list element.
func printEffective(res settings.Resolution) {
	if k, _ := settings.Lookup(res.Key); k != nil && k.IsList() {
		for _, v := range res.List {
			fmt.Println(v)
		}
		return
	}
	fmt.Println(res.Value)
}
