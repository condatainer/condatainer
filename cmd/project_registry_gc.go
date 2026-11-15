package cmd

import (
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/publish"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/spf13/cobra"
)

func newProjectRegistryGCCmd() *cobra.Command {
	var gitHistory bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove published overlays the project no longer pins",
		Long: `Remove overlays from the project's registry that its lock does not pin.

- The project must pass ` + "`condatainer project validate`" + `, or nothing is removed.
- An older commit of the project can no longer download an overlay removed here.`,
		Example: `  condatainer project registry gc
  condatainer project registry gc --git-history`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			l, err := lock.Load(root)
			if err != nil {
				return err
			}
			if l.OCI.Empty() {
				return fmt.Errorf("no registry is recorded; set one with `condatainer project registry set <registry>/<owner>/<repo>`")
			}
			_, problems := verifyLock(root, l)
			scanned, err := lock.Scan(root, lock.ScanOptions{})
			if err != nil {
				return err
			}
			if invalid := projectProblems(l, scanned, problems); len(invalid) > 0 {
				return fmt.Errorf("the project is not valid, so nothing was removed:\n  %s", strings.Join(invalid, "\n  "))
			}
			repository := registry.TrimBaseScheme(l.OCI.Push)
			cat, err := config.OpenCatalog(cmd.Context())
			if err != nil {
				return fmt.Errorf("cannot read the configured sources: %w", err)
			}
			if name := sourceDeclaring(cat, repository); name != "" {
				return fmt.Errorf("%s is the registry of source %s, not a project's; use `condatainer registry gc --source %s`", repository, name, name)
			}

			pinned := publish.Pinned(l, repository)
			if gitHistory {
				if err := publish.HistoricPinned(cmd.Context(), root, repository, pinned); err != nil {
					return err
				}
			}
			base, repo, err := registry.SplitCoordinate(repository)
			if err != nil {
				return err
			}
			backend, err := registry.BackendFor(base, repo)
			if err != nil {
				return err
			}
			plan, err := registry.PlanUnpinned(cmd.Context(), backend, pinned)
			if err != nil {
				return err
			}
			return finishRemoval(cmd, cmd.Context(), repository, backend, plan)
		},
	}
	jsonFlag(cmd.Flags(), &registryRemoveJSON)
	cmd.Flags().BoolVar(&gitHistory, "git-history", false, "Also keep what earlier commits on this branch pin (needs a full clone)")
	return cmd
}
