package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/registry"
	"github.com/spf13/cobra"
)

func newRegistryRemoveCommand(opts *registryOptions) *cobra.Command {
	remove := &cobra.Command{
		Use:   "remove [flags] <name|name@identity>",
		Short: "Remove published overlays from a source's registry",
		Long: `Remove published overlays from a source's registry.

- name@identity removes one build on every architecture.
- A name alone removes all builds.
- The name moves to the newest other build if it installed the removed one.
- A project that pins a removed build can no longer download it.`,
		Example: `  # One build, every architecture
  condatainer registry remove star/2.7.11b@9f2c1ab --source lab
  # All builds, arm64 only
  condatainer registry remove star/2.7.11b --source lab --arch arm64`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := parseRemoveTarget(args[0], opts.arch)
			if err != nil {
				return err
			}
			return runRemoval(cmd, opts, func(ep registryEndpoint, base, repo string, b registry.Backend) (registry.RemovePlan, error) {
				return registry.PlanRemove(ep.ctx, base, repo, b, target)
			})
		},
	}
	addSourceOnlyFlag(remove, opts)
	jsonFlag(remove.Flags(), &registryRemoveJSON)
	remove.Flags().StringVar(&opts.arch, "arch", "", "Only this machine architecture: amd64, arm64 or noarch")
	return remove
}

// parseRemoveTarget reads "name" or "name@identity".
func parseRemoveTarget(spec, arch string) (registry.RemoveTarget, error) {
	addr, err := parseRegistryAddress(spec, arch)
	if err != nil {
		return registry.RemoveTarget{}, err
	}
	if addr.Equiv != "" {
		return registry.RemoveTarget{}, fmt.Errorf("%q is not a build; give a name or name@identity", spec)
	}
	return registry.RemoveTarget{Name: addr.Name, Identity: addr.Identity, Arch: arch}, nil
}
