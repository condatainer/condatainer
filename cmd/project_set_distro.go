package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/orchestrate"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectSelectDistroCmd() *cobra.Command {
	var auto bool
	cmd := &cobra.Command{
		Use:   "set-distro [flags] [distro]",
		Short: "Choose the distro this project uses as its base",
		Long: `Choose the distro whose base image is this project's container root.

- It overrides the configured default_distro for this project.
- --auto removes the override and goes back to default_distro.`,
		Example: `  condatainer project set-distro rocky9
  condatainer project set-distro --auto`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if auto == (len(args) == 1) {
				return fmt.Errorf("give exactly one of a distro or --auto")
			}
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			current, err := lock.Load(root)
			if err != nil {
				return err
			}
			var pinned *lock.Pinned
			if auto {
				pinned, err = lock.AutoDistro(root, current, config.ResolvedDefaultDistro(), lock.PinOptions{})
			} else {
				pinned, err = lock.SelectDistro(root, current, args[0], lock.PinOptions{})
			}
			if err != nil {
				return err
			}
			upstream := orchestrate.RecordUpstream(cmd.Context(), root, current, pinned)
			if err := lock.Publish(root, current); err != nil {
				return err
			}

			if auto {
				utils.PrintSuccess("Root reset to the configured default: %s", pinned.Name)
			} else {
				utils.PrintSuccess("Root pinned to %s", pinned.Name)
			}
			utils.PrintMessage("  identity %s", pinned.Identity.Digest())
			utils.PrintMessage("  read from %s", pinned.Path)
			for _, artifact := range pinned.Vendored {
				line := "  vendored " + artifact
				if remote, ok := upstream[artifact]; ok {
					line += " (fetchable from " + remote + ")"
				}
				utils.PrintMessage("%s", line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&auto, "auto", false, "Remove the override and use the configured default_distro")
	return cmd
}
