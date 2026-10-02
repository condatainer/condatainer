package cmd

import (
	"os"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newStoreUseCmd() *cobra.Command {
	var identity string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "use [flags] <name>@<identity>",
		Short: "Make one build the default for its name",
		Long: `Make one build the one that a plain name resolves to.

- The build takes the plain name, and the build that held it moves into the store.
- Files are only renamed inside their directory. Every build stays reachable.
- A build in a directory shadowed by a nearer one is refused. Promote it in the nearer directory instead.`,
		Example: `  condatainer store use star/2.7.11b@9f2c1ab
  condatainer store use star/2.7.11b --identity 9f2c1ab`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, query, err := splitStoreAddress(args[0], identity)
			if err != nil {
				return err
			}
			if !confirmSharedPromotion(cmd, name, query) {
				return nil
			}
			result, err := store.Promote(name, query, nil)
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(result)
			}
			reportPromotion(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&identity, "identity", "", "Complete identity or unambiguous digest prefix")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

// confirmSharedPromotion asks before changing what a name means for other people,
// and reports whether to go on.
//   - A promotion breaks no project lock — those pin identities, and every identity stays resolvable — but everyone addressing the name gets a different build from then on.
//   - In a personal directory there is nobody else to surprise.
func confirmSharedPromotion(cmd *cobra.Command, name string, query store.IdentityQuery) bool {
	if utils.ShouldAnswerYes() {
		return true
	}
	candidate, _, err := store.ResolveIdentity(name, query, nil)
	if err != nil || candidate.Layout == store.LayoutFlat || config.IsPersonalImagesDir(candidate.Root) {
		// A resolution failure is Promote's to report, with its own message.
		return true
	}
	utils.PrintWarning("%s is shared: everyone reading it gets this build for %s from now on.",
		candidate.Root, candidate.Name)
	return utils.Confirm(cmd.Context(), os.Stdout, "Promote anyway? [y/N]: ")
}

func reportPromotion(result store.Promotion) {
	if result.AlreadyFlat {
		utils.PrintSuccess("%s already resolves to this build (%s).",
			result.Name, store.FormatKeyRef(result.Promoted.Identity))
		return
	}
	utils.PrintSuccess("%s now resolves to %s", result.Name,
		result.Promoted.Path)
	utils.PrintMessage("  identity %s", store.FormatKeyRef(result.Promoted.Identity))
	if result.DemotedTo != "" {
		utils.PrintMessage("  was %s, now at %s",
			store.FormatKeyRef(result.Demoted.Identity), result.DemotedTo)
	}
	// Named because it is still there: a farther copy is shadowed, not removed,
	// and someone reading only that directory still gets it.
	if result.Shadows != "" {
		utils.PrintNote("%s is now shadowed by this one.", result.Shadows)
	}
}
