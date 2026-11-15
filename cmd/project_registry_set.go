package cmd

import (
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/publish"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectRegistrySetCmd() *cobra.Command {
	var audience, codeURL string
	cmd := &cobra.Command{
		Use:   "set [flags] <host>/<owner>/<repo>",
		Short: "Set the publish destination",
		Long: `Record where ` + "`condatainer project registry push`" + ` publishes.

- Give a repository with no tag or digest. Every overlay goes into it.
- A public destination refuses an app whose recipe forbids redistribution.
- --audience only declares who can pull. Nothing enforces it.`,
		Example:      `  condatainer project registry set ghcr.io/my-lab/rnaseq-2026/cnt --audience restricted`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			l, err := lock.Load(root)
			if err != nil {
				return err
			}
			destination, err := catalog.NormalizeRepository(args[0])
			if err != nil {
				return err
			}
			previous := l.OCI
			l.OCI = lock.OCI{
				Push:     destination,
				Audience: strings.ToLower(strings.TrimSpace(audience)),
			}
			fromOrigin, originPort := false, false
			if cmd.Flags().Changed("code-url") {
				l.Source = strings.TrimSpace(codeURL)
			} else if l.Source == "" {
				// Derived once and recorded, never re-derived at push time: two
				// collaborators with different remote spellings would otherwise
				// publish two different annotations for one project.
				l.Source, originPort = publish.OriginURL(cmd.Context(), root)
				fromOrigin = l.Source != ""
			}
			if err := lock.Publish(root, l); err != nil {
				l.OCI = previous
				return err
			}

			utils.PrintSuccess("Publishing to %s", l.OCI.Push)
			utils.PrintMessage("  audience %s", audienceOrDefault(l.OCI.Audience))
			switch {
			case l.Source != "":
				utils.PrintMessage("  source   %s", l.Source)
				if fromOrigin {
					utils.PrintNote("The source was taken from the origin remote. Pass --code-url to use another.")
				}
			case originPort:
				utils.PrintNote("The origin remote names a port. Pass --code-url to record the source.")
			default:
				utils.PrintMessage("  no repository URL was derived from origin; pass --code-url to record one")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&audience, "audience", "public", "Declare who can download from the registry: public or restricted")
	cmd.Flags().StringVar(&codeURL, "code-url", "", "Code repository URL that published packages link to (default: origin remote)")
	return cmd
}
