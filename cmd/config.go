package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	initLayer string // user, cnt-root, extra-root, or a custom path
	getLayer  string // target layer for config get (single-layer read)
	setLayer  string // target layer for config set/append/prepend/remove
)

// configLayersHelp documents the -l/--layer values. Shared by every config
// subcommand that accepts --layer so the list stays consistent, not copy-pasted.
const configLayersHelp = `Config file layers (-l, --layer):
  u, user        ~/.config/condatainer/config.yaml
  e, extra-root  $CNT_EXTRA_ROOT/config.yaml
  r, cnt-root    <cnt-root>/config.yaml
` + cntRootHelp

// cntRootHelp says where cnt-root is.
const cntRootHelp = `<cnt-root> is $CNT_ROOT, or the folder above the bin/ that holds condatainer.
That folder is not used if it is /, /opt, /usr, /usr/local, $HOME or ~/.local.`

// dataDirsHelp says which data directory reads and writes use.
const dataDirsHelp = `- Reads use the first match: user > extra-root > cnt-root.
- Writes use the first writable one: extra-root > cnt-root > user.`

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage condatainer configuration",
	Long: `Manage condatainer configuration.

Each layer has a config file and a data directory:
  layer       config file                        data directory
  user        ~/.config/condatainer/config.yaml  $SCRATCH/condatainer,
                                                 ~/.local/share/condatainer
  extra-root  $CNT_EXTRA_ROOT/config.yaml        $CNT_EXTRA_ROOT
  cnt-root    <cnt-root>/config.yaml             <cnt-root>

` + cntRootHelp + `

Settings, highest priority first:
1. Command-line flags
2. Environment variables (CNT_CONFIG_*)
3. Config files: user > extra-root > cnt-root
4. Defaults

Data directories hold overlays and helper scripts:
` + dataDirsHelp,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configListCmd, configGetCmd, configSetCmd, configAppendCmd, configPrependCmd, configRemoveCmd,
		configInitCmd, configPathsCmd, configHelpCmd, configCheckCmd, configSourceCmd)
}

func isArrayKey(key string) bool {
	k, ok := settings.Lookup(key)
	return ok && k.IsList()
}

// refuseSourcesKey exits when key is `sources`, which `config source` manages.
func refuseSourcesKey(key string) {
	if key == "sources" {
		ExitWithError("sources are managed with `condatainer config source add|move|remove`")
	}
}

// modifyArrayConfig applies modify to key's list in the writable config file
// and returns that file's layer.
func modifyArrayConfig(key string, modify func([]string) []string) (string, error) {
	refuseSourcesKey(key)
	if !isArrayKey(key) {
		var arrayKeys []string
		for k, isArr := range knownConfigKeys() {
			if isArr {
				arrayKeys = append(arrayKeys, k)
			}
		}
		sort.Strings(arrayKeys)
		return "", fmt.Errorf("'%s' is not an array config key; array keys: %s",
			key, strings.Join(arrayKeys, ", "))
	}
	configPath, layer, fellBackFrom, err := config.ResolveWritableConfigPathVerbose(setLayer)
	if err != nil {
		return "", err
	}
	if fellBackFrom != "" {
		utils.PrintWarning("%s config is read-only; saving to your user config instead (applies only to you).", fellBackFrom)
	}
	current := config.ReadConfigSliceKey(configPath, key)
	updated := modify(current)
	if len(updated) == 0 {
		return layer, config.DeleteConfigKey(configPath, key)
	}
	return layer, config.UpdateConfigKey(configPath, key, updated)
}

// parseArrayValue validates one element of an array key and returns its stored form.
func parseArrayValue(key, value string) string {
	if k, ok := settings.Lookup(key); ok && isArrayKey(key) {
		return parseRegistered(k, value)
	}
	return value
}

func arrayKeyCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		var keys []string
		for k, isArr := range knownConfigKeys() {
			if isArr {
				keys = append(keys, k)
			}
		}
		return keys, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		switch args[0] {
		}
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// configValueCompletion returns suggested values for a config key
func configValueCompletion(key string) []string {
	if k, ok := settings.Lookup(key); ok {
		return k.Suggest()
	}
	return nil
}

// knownConfigKeys maps every registered config key to whether it holds a list.
func knownConfigKeys() map[string]bool {
	out := map[string]bool{}
	for _, k := range settings.Keys() {
		out[k.Name] = k.IsList()
	}
	return out
}

// originTag names where a resolved value comes from.
func originTag(res settings.Resolution) string {
	switch res.Source {
	case settings.SourceFlag:
		return "  " + utils.StyleWarning("[flag: --"+res.Flag+"]")
	case settings.SourceEnv:
		return "  " + utils.StyleWarning("[env: "+res.Env+"]")
	case settings.SourceLayer:
		return "  " + utils.StyleSuccess("["+res.Layer+"]")
	}
	return "  " + utils.StyleDim("[default]")
}

// parseRegistered validates value for a registered key and returns the form to store.
func parseRegistered(k *settings.Key, value string) string {
	stored, err := k.Parse(value)
	if err != nil {
		utils.PrintError("Invalid value: %v", err)
		utils.PrintHint("%s takes %s", k.Name, k.Accepts())
		os.Exit(ExitCodeError)
	}
	if err := k.Check(stored); err != nil {
		utils.PrintError("%s: %v", k.Name, err)
		os.Exit(ExitCodeError)
	}
	return stored
}

// registerSettingFlagCompletions gives every flag made from a key the key's completion values.
func registerSettingFlagCompletions(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		k, ok := settings.FlagKey(f)
		if !ok || len(k.Suggest()) == 0 || f.NoOptDefVal != "" {
			return
		}
		values := k.Suggest()
		cmd.RegisterFlagCompletionFunc(f.Name, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) { //nolint:errcheck
			return values, cobra.ShellCompDirectiveNoFileComp
		})
	})
}
