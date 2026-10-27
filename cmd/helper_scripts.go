package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	helperSources []string
	helperLayer   string
	helperForce   bool
)

var helperAvailCmd = &cobra.Command{
	Use:   "avail [flags] [terms...]",
	Short: "List the helpers the sources offer",
	Long: `List the helpers the sources offer.

- Each line shows whether the helper is added and whether a newer version is offered.

` + searchSyntaxHint,
	Example: `  condatainer helper avail
  condatainer helper avail server
  condatainer helper avail --source lab`,
	SilenceUsage: true,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return config.SelectSources(helperSources)
	},
	RunE: runHelperAvail,
}

var helperAddCmd = &cobra.Command{
	Use:   "add [flags] <name>...",
	Short: "Add helpers from the sources",
	Long: `Add helpers from the sources.

- The layer they go in is printed.
- A name already added is refused, and so is a file there that add did not install.
- When several sources offer a name, the earliest wins.`,
	Example: `  condatainer helper add code-server
  condatainer helper add code-server rstudio-server -l e
  condatainer helper add code-server --force`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return config.SelectSources(helperSources)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		refuseInsideJob(cmd)
		return helper.Add(cmd.Context(), args, helperLayerFlag(cmd), helperForce, os.Stdout)
	},
}

var helperRemoveCmd = &cobra.Command{
	Use:   "remove [flags] <name>...",
	Short: "Remove added helpers",
	Long: `Remove helpers that add installed.

- It lists what it will delete and asks first.
- A script you wrote yourself is left in place.
- Saved defaults and run history stay.`,
	Example: `  condatainer helper remove code-server
  condatainer helper remove code-server -l user`,
	Args:              cobra.MinimumNArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: completeHelperScripts,
	RunE:              runHelperRemove,
}

var helperUpgradeCmd = &cobra.Command{
	Use:   "upgrade [flags] [name...]",
	Short: "Bring added helpers up to date",
	Long: `Bring added helpers up to date.

- It prints what it will replace, layer by layer, and asks before it does.
- Without a terminal and without -y, it prints the plan and changes nothing.
- A helper you edited, one no source offers any more, and one in a layer you cannot write are kept and reported.`,
	Example: `  condatainer helper upgrade
  condatainer helper upgrade code-server
  condatainer helper upgrade -l extra-root -y`,
	SilenceUsage:      true,
	ValidArgsFunction: completeHelperScripts,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return config.SelectSources(helperSources)
	},
	RunE: runHelperUpgrade,
}

var (
	helperListOne         bool
	helperListDescription bool
)

var helperListCmd = &cobra.Command{
	Use:     "list [flags] [terms...]",
	Aliases: []string{"ls"},
	Short:   "List installed helpers",
	Long: `List installed helpers grouped by directory.

- A helper hidden by a nearer copy of the same name is marked.

` + searchSyntaxHint,
	Example: `  condatainer helper list
  condatainer helper list server
  condatainer helper list -l user --desc`,
	SilenceUsage: true,
	RunE:         runHelperList,
}

var helperPathCmd = &cobra.Command{
	Use:   "path [name]",
	Short: "Show the helper script directories",
	Long: `Show the directories helpers are read from, nearest first.

- With a name, it prints the file that name resolves to.`,
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: completeHelperScripts,
	RunE:              runHelperPath,
}

func init() {
	helperCmd.AddCommand(helperAvailCmd, helperAddCmd, helperRemoveCmd, helperUpgradeCmd, helperListCmd, helperPathCmd)

	for _, c := range []*cobra.Command{helperAvailCmd, helperAddCmd, helperUpgradeCmd} {
		c.Flags().StringArrayVarP(&helperSources, "source", "s", nil, "Use only this configured source, in flag order (repeatable)")
		c.RegisterFlagCompletionFunc("source", sourceHandleCompletion) //nolint:errcheck
	}
	helperAddCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Layer to add to: "+layerChoices+" (default: the furthest writable layer)")
	helperRemoveCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Layer to remove from: "+layerChoices+" (default: the nearest added copy)")
	helperUpgradeCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Limit to a layer: "+layerChoices+" (default: every layer)")
	helperListCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Limit to a layer: "+layerChoices)
	helperListCmd.Flags().BoolVarP(&helperListOne, "one", "1", false, "One entry per line (disable multi-column layout)")
	helperListCmd.Flags().BoolVar(&helperListDescription, "desc", false, "Show the description of each helper")
	helperAddCmd.Flags().BoolVar(&helperForce, "force", false, "Replace a file add did not install, or an edited one, and record it")
}

// helperLayerFlag reads --layer, or exits on a name that is not a layer.
func helperLayerFlag(cmd *cobra.Command) config.DataLayer {
	if helperLayer == "" {
		return ""
	}
	layer, err := config.ParseDataLayer(helperLayer)
	if err != nil {
		cmd.SilenceUsage = true
		ExitWithError("%v", err)
	}
	return layer
}

func runHelperAvail(cmd *cobra.Command, args []string) error {
	refuseInsideJob(cmd)
	offers, err := helper.RefreshRemoteMetadata(cmd.Context(), false, os.Stdout)
	if err != nil {
		return fmt.Errorf("failed to read helper indexes: %w", err)
	}
	plan, err := helper.PlanUpgradeWith(offers, "", nil)
	if err != nil {
		return err
	}
	state := map[string]string{}
	for _, item := range plan {
		if state[item.Name] == "newer" {
			continue
		}
		state[item.Name] = "added"
		if item.Action == helper.UpgradeReplace || item.Action == helper.UpgradeUnwritable {
			state[item.Name] = "newer"
		}
	}

	query := NewSearchQuery(normalizeFilters(args), nil)
	var names []string
	width := 0
	for name, e := range offers {
		if query.MatchesOrAliasWithText(name, "", e.Description) {
			names = append(names, name)
			width = max(width, len(name))
		}
	}
	if len(names) == 0 {
		utils.PrintWarning("No matching helpers found.")
		return nil
	}
	sort.Strings(names)
	for _, name := range names {
		tag := ""
		switch state[name] {
		case "added":
			tag = utils.StyleDim("[added]")
		case "newer":
			tag = utils.StyleDim("[added, newer version offered]")
		}
		line := fmt.Sprintf("  %-*s", width, name)
		if tag != "" {
			line += "  " + tag
		}
		if d := offers[name].Description; d != "" {
			line += "  " + d
		}
		fmt.Println(line)
	}
	return nil
}

func runHelperRemove(cmd *cobra.Command, args []string) error {
	refuseInsideJob(cmd)
	items, planErr := helper.PlanRemove(args, helperLayerFlag(cmd))
	if len(items) == 0 {
		return planErr
	}
	if planErr != nil {
		utils.PrintWarning("%v", planErr)
	}
	for _, item := range items {
		fmt.Printf("  remove  %s%s\n", item.Path, config.LayerNote(item.Path))
	}
	if !utils.ShouldAnswerYes() {
		if !utils.IsInteractiveShell() {
			return fmt.Errorf("`condatainer helper remove` asks first, and there is no terminal here; run it in a terminal or pass `-y`")
		}
		if !utils.Confirm(cmd.Context(), os.Stdout, fmt.Sprintf("Remove %d helper(s)? [y/N]: ", len(items))) {
			return nil
		}
	}
	err := helper.Remove(items, os.Stdout)
	if planErr != nil {
		return fmt.Errorf("some helpers were not removed")
	}
	return err
}

func runHelperUpgrade(cmd *cobra.Command, args []string) error {
	refuseInsideJob(cmd)
	ctx := cmd.Context()
	plan, err := helper.PlanUpgrade(ctx, args, helperLayerFlag(cmd), os.Stdout)
	if err != nil {
		return err
	}
	replace := printUpgradePlan(plan)
	if replace == 0 {
		return nil
	}
	if !utils.ShouldAnswerYes() {
		if !utils.IsInteractiveShell() {
			return fmt.Errorf("`condatainer helper upgrade` asks first, and there is no terminal here; run it in a terminal or pass `-y`")
		}
		if !utils.ConfirmDefaultYes(ctx, os.Stdout, fmt.Sprintf("Replace %d helper(s)? [Y/n] ", replace)) {
			return nil
		}
	}
	return helper.ApplyUpgrade(ctx, plan, os.Stdout)
}

// printUpgradePlan prints what an upgrade will do layer by layer and returns how many helpers it replaces.
//   - A helper that is already current is counted, not listed.
func printUpgradePlan(plan []helper.UpgradeItem) int {
	replace, current, kept := 0, 0, 0
	lastDir := ""
	for _, item := range plan {
		if item.Action == helper.UpgradeCurrent {
			current++
			continue
		}
		if item.Dir != lastDir {
			lastDir = item.Dir
			fmt.Printf("%s (%s)\n", item.Layer, item.Dir)
		}
		switch item.Action {
		case helper.UpgradeReplace:
			replace++
			fmt.Printf("  replace  %s\n", item.Name)
		case helper.UpgradeEdited:
			kept++
			fmt.Printf("  keep     %s: edited since it was added; `condatainer helper add --force %s` replaces it\n", item.Name, item.Name)
		case helper.UpgradeDropped:
			kept++
			fmt.Printf("  keep     %s: no source offers it any more\n", item.Name)
		case helper.UpgradeUnwritable:
			kept++
			fmt.Printf("  keep     %s: you cannot write this layer; whoever can runs `condatainer helper upgrade -l %s`\n", item.Name, item.Layer)
		}
	}
	switch {
	case replace == 0 && kept == 0 && current > 0:
		utils.PrintMessage("Everything added is up to date.")
	case replace == 0 && len(plan) == 0:
		utils.PrintMessage("No helpers added. See `condatainer helper avail`.")
	}
	return replace
}

func runHelperList(cmd *cobra.Command, args []string) error {
	groups, err := helper.ListScripts()
	if err != nil {
		return err
	}
	layer := helperLayerFlag(cmd)
	filters := normalizeFilters(args)
	query := NewSearchQuery(filters, nil)
	width := terminalWidth()
	if helperListOne || helperListDescription {
		width = 0
	}

	shown := false
	for _, g := range groups {
		if layer != "" && g.Layer != layer {
			continue
		}
		var matched []helper.ListedScript
		for _, s := range g.Scripts {
			if query.MatchesOrAliasWithText(s.Name, "", s.Description) {
				matched = append(matched, s)
			}
		}
		if len(filters) > 0 && len(matched) == 0 {
			continue
		}
		if shown {
			fmt.Println()
		}
		shown = true
		fmt.Println(dirHeader(g.Dir))
		if len(matched) == 0 {
			fmt.Println("  (no helpers)")
			continue
		}
		printHelpers(matched, width)
	}
	if !shown && len(filters) > 0 {
		utils.PrintWarning("No installed helpers match the provided search terms.")
		os.Exit(ExitCodeError)
	}
	if !shown {
		utils.PrintMessage("No helpers installed. See `condatainer helper avail`.")
	}
	return nil
}

// printHelpers prints a directory's helpers in columns or, with --desc, one per entry.
func printHelpers(scripts []helper.ListedScript, width int) {
	var plain, styled []string
	for _, s := range scripts {
		mark := ""
		if s.HiddenBy != "" {
			mark = " (hidden)"
			if helperListDescription {
				mark = " (hidden by " + s.HiddenBy + ")"
			}
		}
		if helperListDescription {
			printListDescription(utils.StyleName(s.Name)+utils.StyleDim(mark), s.Description)
			continue
		}
		plain = append(plain, s.Name+mark)
		styled = append(styled, s.Name+utils.StyleDim(mark))
	}
	printColumns(plain, styled, 0, width)
}

func runHelperPath(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		scriptPath, err := config.FindHelperScript(args[0])
		if err != nil {
			cmd.SilenceUsage = true
			ExitWithError("helper script '%s' not found (searched: %s)",
				args[0], strings.Join(config.GetHelperScriptSearchPaths(), ", "))
		}
		fmt.Println(scriptPath)
		return nil
	}
	fmt.Println("Helper script search paths (priority order):")
	for i, dir := range config.GetHelperScriptSearchPaths() {
		exists := ""
		if !config.DirExists(dir) {
			exists = " (not found)"
		}
		fmt.Printf("  %d. %s%s\n", i+1, dir, exists)
	}
	if writableDir := config.PeekWritableHelperScriptsDir(); writableDir != "" {
		fmt.Printf("\nWritable directory: %s\n", writableDir)
	}
	return nil
}
