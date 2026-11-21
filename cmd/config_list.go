package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"show", "ls"},
	Args:    cobra.NoArgs,
	Short:   "List current configuration",
	Long: `List the current settings and where each comes from.

- The config files searched: user > extra-root > cnt-root.
- Every value.
- Environment variable overrides.
- Use ` + "`condatainer config paths`" + ` for the data directories.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Show config file search paths
		fmt.Println(utils.StyleTitle("Config File Search Paths:"))
		searchPaths := config.GetConfigSearchPaths()
		foundActive := false
		for i, sp := range searchPaths {
			status := ""
			if sp.InUse {
				foundActive = true
			} else if !sp.Exists {
				status = " " + utils.StyleWarning("(not found)")
			}
			fmt.Printf("  %d. [%s] %s%s\n", i+1, sp.Type, sp.Path, status)
		}
		if !foundActive {
			fmt.Printf("  %s (use `condatainer config init` to create)\n", utils.StyleWarning("No config file found"))
		}
		fmt.Println()

		// Remote sources
		fmt.Println(utils.StyleTitle("Recipe Sources:"))
		if len(config.Global.Sources) > 0 {
			fmt.Printf("  sources:\n")
			for _, src := range config.Global.Sources {
				fmt.Printf("    - %s: %s\n", src.Name, src.Base)
			}
		} else {
			fmt.Printf("  sources: %s\n", "none configured")
		}
		fmt.Println()

		fmt.Println(utils.StyleTitle("Options:"))
		top := settings.TopLevel()
		width := labelWidth(top, "")
		for _, k := range top {
			if k.IsList() {
				printListSetting(k.Name, width)
			} else {
				printSetting(k.Name, "", width)
			}
			if k.Name == "home_override" {
				printDistroNote(cmd.Context(), width)
			}
		}
		fmt.Println()

		fmt.Printf("%s %s\n", utils.StyleTitle("Build Configuration:"), "build.*")
		printSection("build")
		fmt.Println()

		fmt.Printf("%s %s\n", utils.StyleTitle("Scheduler Configuration:"), "scheduler.*")
		printSection("scheduler")
		fmt.Println()

		fmt.Printf("%s %s\n", utils.StyleTitle("Server Configuration:"), "server.*")
		printSection("server")
		fmt.Println()

		fmt.Printf("%s %s\n", utils.StyleTitle("Helper Configuration:"), "helper.*")
		printSection("helper")
		fmt.Println()

		// Show environment variable overrides
		fmt.Println(utils.StyleTitle("Environment Variable Overrides:"))
		envVars := getConfigEnvVars()
		hasEnvOverrides := false
		// Special vars not derived from config keys (system → group → personal)
		for _, envVar := range []string{"CNT_ROOT", "CNT_LIBEXEC", "CNT_EXTRA_ROOT", "SCRATCH"} {
			if val := os.Getenv(envVar); val != "" {
				fmt.Printf("  %s=%s\n", envVar, val)
				hasEnvOverrides = true
			}
		}
		for _, envVar := range envVars {
			if val := os.Getenv(envVar); val != "" {
				fmt.Printf("  %s=%s\n", envVar, val)
				hasEnvOverrides = true
			}
		}
		if !hasEnvOverrides {
			fmt.Printf("  %s\n", "none")
		}
	},
}

var showOrigin bool

func init() {
	configListCmd.Flags().BoolVar(&showOrigin, "origin", false, "Tag each value with its layer, environment variable or [default]")
}

// getConfigEnvVars returns a sorted list of CNT_CONFIG_* environment variables for all known config keys.
func getConfigEnvVars() []string {
	known := knownConfigKeys()
	vars := make([]string, 0, len(known))
	for key := range known {
		vars = append(vars, settings.EnvName(key))
	}
	sort.Strings(vars)
	return vars
}

// labelWidth is the width of the label column for keys under prefix: the longest name, its colon and one space.
func labelWidth(keys []*settings.Key, prefix string) int {
	width := 0
	for _, k := range keys {
		width = max(width, len(strings.TrimPrefix(k.Name, prefix))+2)
	}
	return width
}

// printSection prints every registered key under prefix, with the labels aligned.
func printSection(prefix string) {
	keys := settings.Section(prefix)
	width := labelWidth(keys, prefix+".")
	for _, k := range keys {
		printSetting(k.Name, prefix+".", width)
	}
}

// printSetting prints one registered key as a row of `config list`: the label is the key
// without prefix, padded to width. With --origin the row carries its source and the layers it hides.
func printSetting(key, prefix string, width int) {
	k, ok := settings.Lookup(key)
	if !ok {
		return
	}
	res, _ := settings.Resolve(key)
	if k.OmitsEmpty() && res.Value == "" {
		return
	}
	mark := ""
	if k.Deprecation() != nil {
		mark = " " + utils.StyleDim("(deprecated)")
	}
	fmt.Printf("  %-*s %s%s%s\n", width, strings.TrimPrefix(key, prefix)+":", k.Show(res.Value), mark, sourceTag(res))
	if !showOrigin {
		return
	}
	for _, e := range res.Overridden {
		suffix := "(overridden)"
		if res.Source == settings.SourceEnv || res.Source == settings.SourceFlag {
			suffix = "(overridden by " + map[settings.Source]string{settings.SourceEnv: "env", settings.SourceFlag: "flag"}[res.Source] + ")"
		}
		fmt.Printf("  %-*s %s\n", width, "", utils.StyleDim(e.Value+" ["+e.Layer+"] "+suffix))
	}
}

// printListSetting prints a registered list key as a label followed by one line per element.
// With --origin each element carries the source of the list.
func printListSetting(key string, width int) {
	res, ok := settings.Resolve(key)
	if !ok {
		return
	}
	if len(res.List) == 0 {
		fmt.Printf("  %-*s\n", width, key+":")
		return
	}
	fmt.Printf("  %-*s\n", width, key+":")
	for i, e := range res.List {
		tag := ""
		if showOrigin && res.Source != settings.SourceDefault && i < len(res.Origins) {
			style := utils.StyleSuccess
			if res.Origins[i] == "env" || res.Origins[i] == "flag" {
				style = utils.StyleWarning
			}
			tag = "  " + style("["+res.Origins[i]+"]")
		}
		fmt.Printf("    - %s%s\n", e, tag)
	}
}

// sourceTag is the --origin annotation for a resolved value, empty without --origin.
func sourceTag(res settings.Resolution) string {
	if !showOrigin {
		return ""
	}
	return originTag(res)
}

// printDistroNote tells the user when the recipe sources recommend a different default distro than the one configured.
func printDistroNote(ctx context.Context, width int) {
	cat, err := config.OpenCatalog(ctx)
	if err != nil {
		return
	}
	if def := config.SourceDefaultDistro(cat); def != "" && def != config.ResolvedDefaultDistro() {
		fmt.Printf("  %-*s sources now recommend %s — `config set default_distro %s` to switch\n", width, "", def, def)
	}
}
