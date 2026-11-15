package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectUnpinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unpin [flags] <request>",
		Short: "Remove a manual pin",
		Long: `Remove a manual pin, one that no script declares.

- To drop any other pin, remove its #DEP: line and run ` + "`project lock`" + `.
- The overlay file itself is never deleted.`,
		Example: `  condatainer project unpin overlays/env.sqf
  condatainer project unpin ubuntu24/build-essential`,
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
			request, err := lock.CanonicalRequest(args[0])
			if err != nil {
				return err
			}
			entry, pinned := l.Pins[request]
			if !pinned {
				return fmt.Errorf("nothing pins %s", request)
			}
			if !entry.Manual {
				return fmt.Errorf("%s is pinned from a project declaration; remove the declaration and run `condatainer project lock`", request)
			}

			// Reachability before and after says what the removal orphans.
			// Publish prunes exactly that, so this reports rather than decides.
			before, _ := lock.Verify(root, l)
			delete(l.Pins, request)
			after, _ := lock.Verify(root, l)
			if err := lock.Publish(root, l); err != nil {
				return err
			}

			utils.PrintSuccess("Unpinned %s", request)
			for _, artifact := range orphaned(before, after) {
				utils.PrintMessage("  pruned %s", artifact)
			}
			if destination, ok := strings.CutPrefix(request, lock.PathPrefix); ok {
				utils.PrintMessage("  %s is left where it is", destination)
			}
			return nil
		},
	}
	return cmd
}

// orphaned lists the readable artifacts a removal left unreachable, sorted.
// Unreadable ones are left out because Prune leaves them on disk.
func orphaned(before, after *lock.Verified) []string {
	var out []string
	for artifact := range before.Reachable {
		if after.Reachable[artifact] {
			continue
		}
		if _, readable := before.Entries[artifact]; readable {
			out = append(out, artifact)
		}
	}
	sort.Strings(out)
	return out
}
