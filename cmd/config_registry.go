package cmd

import (
	"context"
	"fmt"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
)

// knownConfigKeys maps every registered config key to whether it holds a list.
func knownConfigKeys() map[string]bool {
	out := map[string]bool{}
	for _, k := range settings.Keys() {
		out[k.Name] = k.IsList()
	}
	return out
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
		fmt.Printf("  %-*s %s\n", width, key+":", "none")
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

// effectiveList is the list a registered key holds now.
func effectiveList(key string) []string {
	res, _ := settings.Resolve(key)
	return res.List
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
