package cmd

import (
	"sort"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configRemoveCmd = &cobra.Command{
	Use:     "remove [flags] <key> [value]",
	Aliases: []string{"rm"},
	Short:   "Remove a config key or a value from an array key",
	Long: `Remove a config key entirely, or one value from an array config key.

` + configLayersHelp,
	Example: `  condatainer config remove host_apptainer  # The whole key
  condatainer config remove bind /scratch   # One value of a list`,
	Args:              cobra.RangeArgs(1, 2),
	ValidArgsFunction: arrayRemoveValueCompletion,
	SilenceUsage:      true,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		refuseSourcesKey(key)
		if len(args) == 1 {
			// Scalar removal: unset the key entirely
			if isArrayKey(key) {
				ExitWithError("'%s' is an array key; specify a value to remove: config remove %s <value>", key, key)
			}
			configPath, layer, err := config.ResolveWritableConfigPath(setLayer)
			if err != nil {
				ExitWithError("%v", err)
			}
			if config.ReadConfigKey(configPath, key) == "" {
				utils.PrintWarning("%s is not set in the %s layer", key, utils.StyleName(layer))
				return
			}
			if err := config.DeleteConfigKey(configPath, key); err != nil {
				ExitWithError("%v", err)
			}
			utils.PrintMessage("Removed %s (%s layer)", key, utils.StyleName(layer))
			return
		}
		// Array removal
		value := args[1]
		removed := false
		layer, err := modifyArrayConfig(key, func(cur []string) []string {
			var out []string
			for _, v := range cur {
				if v == value {
					removed = true
					continue
				}
				out = append(out, v)
			}
			return out
		})
		if err != nil {
			ExitWithError("%v", err)
		}
		if removed {
			utils.PrintMessage("Removed %s from %s (%s layer)", value, key, utils.StyleName(layer))
		} else {
			utils.PrintWarning("%s not found in %s (%s layer)", value, key, utils.StyleName(layer))
		}
	},
}

func init() {
	configRemoveCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Layer to write: "+layerChoices)
}

func arrayRemoveValueCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	loc, _ := cmd.Flags().GetString("layer")
	if len(args) == 0 {
		// Suggest only keys that are actually set in the target config file.
		if configPath, _, err := config.ResolveWritableConfigPath(loc); err == nil {
			v := config.ReadConfigAllKeys(configPath)
			if len(v) > 0 {
				sort.Strings(v)
				return v, cobra.ShellCompDirectiveNoFileComp
			}
		}
		// Fallback: all known keys
		var keys []string
		for k := range knownConfigKeys() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 && isArrayKey(args[0]) {
		// Suggest values present in the target config file for this array key.
		if configPath, _, err := config.ResolveWritableConfigPath(loc); err == nil {
			return config.ReadConfigSliceKey(configPath, args[0]), cobra.ShellCompDirectiveNoFileComp
		}
		return effectiveList(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// effectiveList is the list a registered key holds now.
func effectiveList(key string) []string {
	res, _ := settings.Resolve(key)
	return res.List
}
