package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/spf13/cobra"
)

func newRegistryListCommand() *cobra.Command {
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List stored registry credentials",
		Long: `List the saved registry credentials with their layer and username.

- FROM is login for a saved login, or the source a token was added with.
- Passwords and tokens are never shown.
- GITHUB_TOKEN, when set, is used for ghcr.io when none of these matches.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stored, err := registryCredentials()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(stored) == 0 {
				fmt.Fprintln(out, "No stored registry credentials.")
			} else {
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "REGISTRY\tFROM\tUSER\tLAYER\tPERMISSION")
				for _, c := range stored {
					from := "login"
					if c.Source != "" {
						from = "source " + c.Source
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Key, from, c.Username, c.Layer, c.Perm)
				}
				tw.Flush()
				reported := map[string]bool{}
				for _, c := range stored {
					if !reported[c.Path] {
						reported[c.Path] = true
						printFindings(credential.Findings(c.Layer, c.Path))
					}
				}
			}
			if os.Getenv(registry.EnvGitHubToken) != "" {
				fmt.Fprintf(out, "%s is set and applies to ghcr.io when no saved credential matches.\n", registry.EnvGitHubToken)
			}
			return nil
		},
	}
	return list
}
