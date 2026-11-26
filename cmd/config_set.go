package cmd

import (
	"context"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configSetCmd = &cobra.Command{
	Use:   "set [flags] <key> <value>...",
	Short: "Set a configuration value",
	Long: `Set a configuration value and save it to a config file.

An array key takes one or more values and they replace the whole list.

A time or a memory is saved with its unit: build.time 4 saves 4h.
  - Time: 4 (hours), 1.5, 12:30, 12:30:15, 1-12:00:00, 2h30m, 90m
  - Memory: 16 (GiB), 1.5G, 512M, 2T

` + configLayersHelp,
	Example: `  condatainer config set host_apptainer /usr/bin/apptainer
  condatainer config set build.ncpu 8
  condatainer config set build.time 4
  condatainer config set scheduler.submit_job false
  condatainer config set scheduler.exclude_nodes node1 node2`,
	Args:              cobra.MinimumNArgs(2),
	ValidArgsFunction: configSetKeysCompletion,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		values := args[1:]
		for _, v := range values {
			if strings.TrimSpace(v) == "" {
				ExitWithError("value cannot be empty")
			}
		}

		refuseSourcesKey(key)
		if k, ok := settings.Lookup(key); ok {
			if !isArrayKey(key) && len(values) > 1 {
				ExitWithError("'%s' takes one value", key)
			}
			for i := range values {
				values[i] = parseRegistered(k, values[i])
			}
		} else if refuseOldName(key) {
			os.Exit(ExitCodeError)
		} else {
			utils.PrintError("Unknown config key: %s", key)
			utils.PrintHint("`condatainer config help` lists the keys")
			os.Exit(ExitCodeError)
		}

		// A default distro must name a recipe that some source actually provides.
		value := values[0]
		if key == "default_distro" {
			if !recipeExists(cmd.Context(), value+"/base") {
				utils.PrintError("No recipe %s/base in any configured source", value)
				os.Exit(ExitCodeError)
			}
		}

		configPath, layerType, fellBackFrom, err := config.ResolveWritableConfigPathVerbose(setLayer)
		if err != nil {
			ExitWithError("%v", err)
		}
		if fellBackFrom != "" {
			utils.PrintWarning("%s config is read-only; saving to your user config instead (applies only to you).", fellBackFrom)
		}
		var stored any = value
		var replaced string
		if isArrayKey(key) {
			stored = values
			replaced = strings.Join(config.ReadConfigSliceKey(configPath, key), ", ")
			value = strings.Join(values, ", ")
		} else {
			replaced = config.ReadConfigKey(configPath, key)
		}
		if err := config.UpdateConfigKey(configPath, key, stored); err != nil {
			ExitWithError("Failed to save config: %v", err)
		}

		utils.PrintMessage("Set %s = %s (%s layer)", key, value, utils.StyleName(layerType))
		if replaced != "" && replaced != value {
			utils.PrintMessage("Replaced %s", replaced)
		}
	},
}

func init() {
	configSetCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Layer to write: "+layerChoices)
}

// scalarConfigKeys returns all non-array config keys.
func scalarConfigKeys() []string {
	var out []string
	for k, isArr := range knownConfigKeys() {
		if !isArr {
			out = append(out, k)
		}
	}
	return out
}

// configSetKeysCompletion returns only scalar (non-array) config keys for `config set`.
func configSetKeysCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return scalarConfigKeys(), cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		return configValueCompletion(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// recipeExists reports whether a module name resolves in any configured source.
func recipeExists(ctx context.Context, name string) bool {
	cat, err := config.OpenCatalog(ctx)
	if err != nil {
		return false
	}
	_, found, err := cat.Lookup(ctx, name)
	return err == nil && found
}

// refuseOldName prints why a renamed or removed key cannot be set and reports whether it was one.
func refuseOldName(key string) bool {
	if k, a, ok := settings.LookupAlias(key); ok {
		utils.PrintError("%s was renamed to %s", a.Old, k.Name)
		utils.PrintHint("condatainer config set %s <value>", k.Name)
		return true
	}
	if r, ok := settings.LookupRemoved(key); ok {
		utils.PrintError("%s", r.Text())
		return true
	}
	return false
}
