package cmd

import (
	"slices"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/orchestrate"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectPinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pin [flags] <request> [identity]",
		Short: "Pin one declaration to an exact overlay",
		Long: `Pin one declaration to an exact overlay.

- name/version:    pin one installed overlay.
- a project .sqf:  record the current identity of that file.
- An identity is a full scheme@sha256:<hex> key, or an unambiguous prefix.
- Give one to choose between installed builds of a name.
- A project path takes none.`,
		Example: `  # One installed build, chosen by identity
  condatainer project pin star/2.7.11b a31f902c12ab
  # A project .sqf file
  condatainer project pin overlays/combined.sqf`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			current, err := lock.Load(root)
			if err != nil {
				return err
			}
			var identity string
			if len(args) > 1 {
				identity = args[1]
			}
			pinned, err := lock.Pin(root, args[0], identity, lock.PinOptions{})
			if err != nil {
				return err
			}
			pinned.Manual = !declaredByAScript(root, pinned.Request)
			upstream := orchestrate.RecordUpstream(cmd.Context(), root, current, pinned)
			if err := lock.Apply(root, current, pinned); err != nil {
				return err
			}

			utils.PrintSuccess("Pinned %s", pinned.Name)
			utils.PrintMessage("  identity %s", pinned.Identity.Digest())
			if pinned.Manual {
				utils.PrintMessage("  no script declares it, so it is recorded as a manual pin")
			}
			utils.PrintMessage("  read from %s", pinned.Path)
			for _, artifact := range pinned.Vendored {
				line := "  vendored " + artifact
				if remote, ok := upstream[artifact]; ok {
					line += " (fetchable from " + remote + ")"
				}
				utils.PrintMessage("%s", line)
			}
			printUnpublishedNote(current, []*lock.Pinned{pinned})
			return nil
		},
	}
	return cmd
}

// declaredByAScript reports whether anything in the project asks for this request,
// which decides whether the pin is manual.
//
//   - It is derived once, at pin time, and recorded: `project lock` sweeps pins the scan no longer produces.
//   - Artifacts reach a project without a #DEP: (a helper's #DEP:, a frozen environment).
//   - A scan that cannot run answers "declared", so the pin stays sweepable and a later lock can correct it.
func declaredByAScript(root, request string) bool {
	scanned, err := lock.Scan(root, lock.ScanOptions{})
	if err != nil {
		return true
	}
	for _, candidate := range scanned.Requests {
		if slices.Contains(candidate.PathCandidates(), request) {
			return true
		}
	}
	return false
}
