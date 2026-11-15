package cmd

import (
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
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

var (
	helperListOne         bool
	helperListDescription bool
)

func init() {
	helperListCmd.Flags().StringVarP(&helperLayer, "layer", "l", "", "Limit to a layer: "+layerChoices)
	helperListCmd.Flags().BoolVarP(&helperListOne, "one", "1", false, "One entry per line (disable multi-column layout)")
	helperListCmd.Flags().BoolVar(&helperListDescription, "desc", false, "Show the description of each helper")
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
