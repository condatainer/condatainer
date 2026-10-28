package cmd

import (
	"os"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/report"
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
			if candidate, shared := sharedPromotion(name, query); shared && !utils.ShouldAnswerYes() {
				if jsonOutput {
					// The question would be asked, so the answer is no: report the
					// build as it is filed, and promote nothing.
					return report.Write(cmd.OutOrStdout(), &report.Promotion{Overlay: storedRecord(candidate)})
				}
				if !confirmSharedPromotion(cmd, candidate) {
					return nil
				}
			}
			result, err := store.Promote(name, query, nil)
			if err != nil {
				return err
			}
			if jsonOutput {
				return report.Write(cmd.OutOrStdout(), promotionDoc(result))
			}
			reportPromotion(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&identity, "identity", "", "Complete identity or unambiguous digest prefix")
	jsonFlag(cmd.Flags(), &jsonOutput)
	return cmd
}

// sharedPromotion reports whether promoting the build an address names changes
// what a name means for other people, and returns that build.
//   - A promotion breaks no project lock — those pin identities, and every identity stays resolvable — but everyone addressing the name gets a different build from then on.
//   - In a personal directory there is nobody else to surprise.
//   - A resolution failure is Promote's to report, with its own message.
func sharedPromotion(name string, query store.IdentityQuery) (store.Candidate, bool) {
	candidate, _, err := store.ResolveIdentity(name, query, nil)
	if err != nil || candidate.Layout == store.LayoutFlat || config.IsPersonalImagesDir(candidate.Root) {
		return candidate, false
	}
	return candidate, true
}

// confirmSharedPromotion asks, on stderr, and reports whether to go on.
func confirmSharedPromotion(cmd *cobra.Command, candidate store.Candidate) bool {
	utils.PrintWarning("%s is shared: everyone reading it gets this build for %s from now on.",
		candidate.Root, candidate.Name)
	return utils.Confirm(cmd.Context(), os.Stderr, "Promote anyway? [y/N]: ")
}

// promotionDoc is the document of a promotion that was made. The demoted build
// is described where it now is.
func promotionDoc(result store.Promotion) *report.Promotion {
	doc := &report.Promotion{
		Overlay:        storedRecord(result.Promoted),
		Shadows:        result.Shadows,
		AlreadyCurrent: result.AlreadyFlat,
		Outcome:        report.OutcomeDone,
	}
	if result.AlreadyFlat {
		doc.Outcome = report.OutcomeSkipped
	}
	if result.DemotedTo != "" {
		demoted := result.Demoted
		demoted.Path, demoted.Layout = result.DemotedTo, store.LayoutStored
		rec := storedRecord(demoted)
		doc.Demoted = &rec
	}
	return doc
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
