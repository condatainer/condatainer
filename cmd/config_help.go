package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configHelpCmd = &cobra.Command{
	Use:   "help [key|section]",
	Args:  cobra.MaximumNArgs(1),
	Short: "Explain a configuration key",
	Long: `Explain what a configuration key does.

- No argument: every key with its default and one line of help.
- A key: what it does, what it takes, its default, its variable, its flags and its value now.
- A section such as scheduler.slurm: every key under it.`,
	ValidArgsFunction: configHelpCompletion,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			printAllHelp()
			return
		}
		if k, ok := settings.Lookup(args[0]); ok {
			printKeyHelp(k)
			return
		}
		if k, a, ok := settings.LookupAlias(args[0]); ok {
			utils.PrintNote("%s", a.RenameMessage())
			printKeyHelp(k)
			return
		}
		if r, ok := settings.LookupRemoved(args[0]); ok {
			ExitWithError("%s", r.Text())
		}
		if keys := settings.Section(args[0]); len(keys) > 0 {
			printKeyList(keys)
			return
		}
		ExitWithError("Unknown config key or section: %s", args[0])
	},
}

// configHelpCompletion suggests every key and every section.
func configHelpCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range settings.Keys() {
		out = append(out, k.Name)
		for dot := strings.LastIndex(k.Name, "."); dot > 0; dot = strings.LastIndex(k.Name[:dot], ".") {
			if section := k.Name[:dot]; !seen[section] {
				seen[section] = true
				out = append(out, section)
			}
		}
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

// defaultShown is the default of a key as `config help` prints it.
func defaultShown(k *settings.Key) string {
	text := k.DefaultText()
	if k.IsList() {
		text = strings.Join(k.DefaultList, ", ")
	}
	if text == "" {
		return "none"
	}
	return text
}

func printAllHelp() {
	var top, grouped []*settings.Key
	for _, k := range settings.Keys() {
		if strings.Contains(k.Name, ".") {
			grouped = append(grouped, k)
		} else {
			top = append(top, k)
		}
	}
	printKeyList(append(top, grouped...))
}

// printKeyList prints each key with its default and one line of help.
func printKeyList(keys []*settings.Key) {
	for _, k := range keys {
		fmt.Printf("%s  %s\n", utils.StyleName(k.Name), utils.StyleDim("(default: "+defaultShown(k)+")"))
		fmt.Printf("    %s\n", k.Help)
	}
}

func printKeyHelp(k *settings.Key) {
	fmt.Println(utils.StyleName(k.Name))
	fmt.Printf("  %s\n", k.Help)
	if text := k.DeprecationText(); text != "" {
		fmt.Printf("  %s\n", utils.StyleWarning(text))
	}
	fmt.Println()
	fmt.Printf("  Takes:    %s\n", k.Accepts())
	fmt.Printf("  Default:  %s\n", defaultShown(k))
	fmt.Printf("  Env:      %s\n", settings.EnvName(k.Name))
	if aliases := k.Aliases(); len(aliases) > 0 {
		olds := make([]string, len(aliases))
		for i, a := range aliases {
			olds[i] = a.Old
		}
		fmt.Printf("  Formerly: %s\n", strings.Join(olds, ", "))
	}
	if flags := k.FlagNames(); len(flags) > 0 {
		for i := range flags {
			flags[i] = "--" + flags[i]
		}
		fmt.Printf("  Flags:    %s\n", strings.Join(flags, ", "))
	}
	if res, ok := settings.Resolve(k.Name); ok {
		value := res.Value
		if k.IsList() {
			value = strings.Join(res.List, ", ")
		}
		if value == "" {
			value = "none"
		}
		fmt.Printf("  Now:      %s%s\n", value, originTag(res))
	}
}
