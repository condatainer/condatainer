package cmd

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectStatusCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show project state and unpinned declarations",
		Long: `Show whether the current directory (or --project DIR) is a project.

- Number of pinned overlays.
- #DEP: declarations that have no pin yet.
- Read-only. Use ` + "`condatainer project lock`" + ` to update the lock.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			// Resolved without walking up, like every other subcommand in this
			// family: a --project directory that looks empty must not reach an
			// ancestor project. Only ambient callers walk up.
			root, err := projectRoot(false)
			if err != nil && !errors.Is(err, lock.ErrNoProject) {
				return err
			}
			if err != nil {
				return printNoProjectStatus(jsonOutput)
			}
			// projectRoot's --project branch takes the directory as named
			// without verifying cnt-lock/ is actually there (RootFor's own
			// contract); StatusAt is what actually checks, so a --project
			// pointing at a bare subdirectory still reports "no project"
			// instead of walking up to an unrelated ancestor.
			status, err := project.StatusAt(root)
			if errors.Is(err, lock.ErrNoProject) {
				return printNoProjectStatus(jsonOutput)
			}
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(status)
			}
			utils.PrintMessage("Project: %s", status.Root)
			utils.PrintMessage("%d pin(s)", status.PinCount)
			if len(status.Unpinned) > 0 {
				utils.PrintWarning("%d declaration(s) not pinned:", len(status.Unpinned))
				for _, key := range status.Unpinned {
					utils.PrintMessage("  %s", key)
				}
				utils.PrintHint("Run `condatainer project lock` to pin them.")
			}
			printUnpinnedHelperOverlays(status.UnpinnedHelperOverlays)
			printManualPinUsage(status.ManualPinUsage)
			printSymlinks(status.SymlinkedScripts, status.SymlinkedDirs)
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &jsonOutput)
	return cmd
}

// printNoProjectStatus reports "no project" for `project status`, in
// whichever format was asked for — shared by projectRoot's ambient
// ErrNoProject and StatusAt's --project-directory ErrNoProject, so the two
// ways of finding out there is no project here read identically.
func printNoProjectStatus(jsonOutput bool) error {
	if jsonOutput {
		return printJSON(&project.ProjectStatus{})
	}
	utils.PrintMessage("No project here.")
	utils.PrintHint("Run `condatainer project lock` to create one.")
	return nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
