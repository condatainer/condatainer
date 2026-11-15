package cmd

import (
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage the publish destination, and publish to it",
		Long: `Show where ` + "`condatainer project registry push`" + ` publishes.

- ` + "`set`" + ` records the destination, and ` + "`unset`" + ` clears it.
- ` + "`push`" + ` publishes to it, and ` + "`gc`" + ` removes what the lock no longer pins.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			root, err := projectRoot(false)
			if err != nil {
				return err
			}
			l, err := lock.Load(root)
			if err != nil {
				return err
			}
			if l.OCI.Empty() {
				utils.PrintMessage("No registry is recorded. Set one with `condatainer project registry set <registry>/<owner>/<repo>`.")
				return nil
			}
			utils.PrintMessage("push     %s", l.OCI.Push)
			utils.PrintMessage("audience %s", audienceOrDefault(l.OCI.Audience))
			if l.Source != "" {
				utils.PrintMessage("source   %s", l.Source)
			}
			return nil
		},
	}
	cmd.AddCommand(newProjectRegistrySetCmd(), newProjectRegistryUnsetCmd(), newProjectRegistryPushCmd(), newProjectRegistryGCCmd())
	return cmd
}
