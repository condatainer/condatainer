package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectSelectMatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-match [flags] <identity|equivalence>",
		Short: "Choose which installed builds this project accepts",
		Long: `Choose how strictly installed overlays must match the project's pins.

- equivalence: an equivalent build is accepted, and reported (default).
- identity: only the pinned build is accepted.

With identity:
- ` + "`project restore`" + ` fetches or rebuilds the exact build.
- ` + "`run`" + ` and ` + "`exec`" + ` stop if only an equivalent build is installed.

The choice is saved in the lock.`,
		Example: `  condatainer project set-match identity     # Require the exact pinned builds
  condatainer project set-match equivalence  # Accept equivalent builds`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := lock.Match(args[0])
			if mode != lock.MatchEquivalence && mode != lock.MatchIdentity {
				return fmt.Errorf("unknown match %q: use %q or %q", args[0], lock.MatchEquivalence, lock.MatchIdentity)
			}
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			current, err := lock.Load(root)
			if err != nil {
				return err
			}
			current.Match = mode
			if mode == lock.MatchEquivalence {
				current.Match = ""
			}
			if err := lock.Publish(root, current); err != nil {
				return err
			}
			utils.PrintSuccess("Project accepts %s copies", mode)
			return nil
		},
	}
}
