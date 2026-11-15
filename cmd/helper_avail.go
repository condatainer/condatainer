package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var helperAvailCmd = &cobra.Command{
	Use:   "avail [flags] [terms...]",
	Short: "List the helpers the sources offer",
	Long: `List the helpers the sources offer.

- Each line shows if the helper is added and if a newer version is offered.

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

func init() {
	sourceFlag(helperAvailCmd)
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
