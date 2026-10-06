package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/condatainer/condatainer/internal/conda"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/condatainer/condatainer/internal/utils"
)

var searchJSON bool
var searchFuzzy bool
var searchChannels []string
var searchLimit int

var searchCmd = &cobra.Command{
	Use:   "search [flags] <package>",
	Short: "Search conda packages via anaconda.org",
	Long: `Search Conda packages via anaconda.org API.

- Results are limited to the configured channels, or to --channel.
- By default the name must match exactly. -f matches approximately.`,
	Example: `  condatainer search samtools              # Exact match
  condatainer search samtools --json       # JSON output
  condatainer search samtools -c bioconda  # One channel
  condatainer search -f samtool            # Fuzzy match
  condatainer search -f samtool -l 200     # Fuzzy match, more results`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Print JSON")
	searchCmd.Flags().BoolVarP(&searchFuzzy, "fuzzy", "f", false, "Fuzzy search: approximate name match")
	searchCmd.Flags().StringArrayVarP(&searchChannels, "channel", "c", nil, "Conda channel to search (overrides config; repeatable)")
	searchCmd.Flags().IntVarP(&searchLimit, "limit", "l", 100, "Fuzzy results to fetch before channel filtering")
}

func platformSupported(platform string, platforms []string) bool {
	for _, p := range platforms {
		if p == platform || p == "noarch" {
			return true
		}
	}
	return false
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := args[0]
	channels := conda.Channels()
	if len(searchChannels) > 0 {
		channels = searchChannels
	}

	results, capped, err := utils.SearchCondaPackages(query, channels, searchFuzzy, searchLimit)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		utils.PrintWarning("No packages found for %q in channels: %s", query, strings.Join(channels, ", "))
		os.Exit(ExitCodeError)
	}

	if searchJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	currentPlatform := utils.CurrentCondaPlatform()
	for i, r := range results {
		if i > 0 {
			fmt.Println()
		}
		archSuffix := ""
		if currentPlatform != "" && len(r.Platforms) > 0 {
			hasPlatform, hasNoarch := false, false
			for _, p := range r.Platforms {
				switch p {
				case currentPlatform:
					hasPlatform = true
				case "noarch":
					hasNoarch = true
				}
			}
			var parts []string
			if hasPlatform {
				parts = append(parts, currentPlatform)
			}
			if hasNoarch {
				parts = append(parts, "noarch")
			}
			if len(parts) > 0 {
				archSuffix = " " + ("[" + strings.Join(parts, "/") + "]")
			}
		}
		fmt.Printf("%s (%s)%s\n", utils.StyleName(r.Name), r.Channel, archSuffix)
		if r.Summary != "" {
			fmt.Printf("  %s\n", r.Summary)
		}
		if len(r.Versions) > 0 {
			printWrapped("Versions:", strings.Join(r.Versions, " "), 0)
		}
		if currentPlatform != "" && len(r.Platforms) > 0 && !platformSupported(currentPlatform, r.Platforms) {
			utils.PrintWarning("Package %s is not available for %s", r.Name, currentPlatform)
		}
	}

	if capped {
		utils.PrintWarning("Results may be incomplete: reached the limit of %d results. Use -l to increase.", searchLimit)
	}

	return nil
}
