package cmd

import (
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectRegistryUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: "Clear the publish destination",
		Long: `Clear the publish destination.

- ` + "`project registry push`" + ` cannot publish until one is set again.
- Published overlays stay where they are, and a restore can still download them.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			l, err := lock.Load(root)
			if err != nil {
				return err
			}
			if l.OCI.Empty() {
				utils.PrintMessage("No registry was recorded.")
				return nil
			}
			was := l.OCI.Push
			l.OCI = lock.OCI{}
			if err := lock.Publish(root, l); err != nil {
				return err
			}
			utils.PrintSuccess("No longer publishing to %s", was)
			return nil
		},
	}
}
