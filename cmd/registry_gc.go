package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newRegistryGCCommand(opts *registryOptions) *cobra.Command {
	gc := &cobra.Command{
		Use:   "gc [flags] [name]",
		Short: "Remove builds no name installs from a source's registry",
		Long: `Remove builds that no name installs from a source's registry.

- A build stays while its name installs it.
- A build also stays while it is the newest built from its recipe.
- A name limits the run to that name.
- A project that pins a removed build can no longer download it.`,
		Example: `  condatainer registry gc --source lab
  condatainer registry gc star/2.7.11b --source lab`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := utils.NormalizeModule(firstArg(args))
			if opts.noRecipe {
				// The recipe index is cached, and a stale one would call a new recipe missing.
				if err := config.RefreshCatalogCache(); err != nil {
					return err
				}
			}
			return runRemoval(cmd, opts, func(ep registryEndpoint, _, _ string, b registry.Backend) (registry.RemovePlan, error) {
				var hasRecipe func(string) bool
				if opts.noRecipe {
					var err error
					if hasRecipe, err = recipeCheck(ep); err != nil {
						return registry.RemovePlan{}, err
					}
				}
				return registry.PlanGC(ep.ctx, b, name, hasRecipe)
			})
		},
	}
	addSourceOnlyFlag(gc, opts)
	jsonFlag(gc.Flags(), &registryRemoveJSON)
	gc.Flags().BoolVar(&opts.noRecipe, "no-recipe", false, "Also remove builds of names the source has no recipe for")
	return gc
}

// recipeCheck reports whether the source has a recipe for a name.
// It refuses when the source cannot say, since every name would look missing.
func recipeCheck(ep registryEndpoint) (func(string) bool, error) {
	source := ep.source
	if source == nil {
		return nil, fmt.Errorf("--no-recipe needs a source")
	}
	entries, err := source.Entries(ep.ctx)
	switch {
	case err != nil:
		return nil, fmt.Errorf("cannot read the recipes of source %s, so none can be called missing: %w", source.Name, err)
	case source.Stale:
		return nil, fmt.Errorf("source %s could not be refreshed, so its recipes may be out of date", source.Name)
	case len(entries) == 0:
		return nil, fmt.Errorf("source %s lists no recipes, so every build would be removed", source.Name)
	}
	only := catalog.Catalog{source}
	return func(name string) bool {
		_, found, _ := only.Lookup(ep.ctx, name)
		return found
	}, nil
}
