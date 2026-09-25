package cmd

import "github.com/spf13/cobra"

// overlayExportHelp is the help body for 'overlay export'.
const overlayExportHelp = `Export the Conda environment of a writable .img overlay.

- It captures the environment as it is now, including packages installed since the overlay was created.
- Installed .sqf overlays are not exportable. Rebuild them from their recipe.
- Output goes to stdout, or to <prefix>.yml (or .txt with --explicit) with --prefix.`

// overlayExportExample is the example block for 'overlay export'.
const overlayExportExample = `  condatainer overlay export ./env.img                 # environment.yml on stdout
  condatainer overlay export ./env.img --from-history  # Only explicitly installed packages
  condatainer overlay export ./env.img -e              # Explicit spec (.txt)
  condatainer overlay export ./env.img -p ./env        # Write ./env.yml`

// registerExportFlags registers the micromamba env-export flags for 'overlay export'.
func registerExportFlags(cmd *cobra.Command) {
	// Keep registration order so --prefix leads, ahead of the Conda-only flags,
	// instead of cobra's alphabetical sort.
	cmd.Flags().SortFlags = false
	cmd.Flags().StringP("prefix", "p", "", "Write to <prefix>.<ext>; extension set by format")
	cmd.Flags().BoolP("explicit", "e", false, "Conda: use explicit format")
	cmd.Flags().Bool("no-md5", false, "Conda: disable md5")
	cmd.Flags().Bool("no-build", false, "Conda: disable the build string in spec")
	cmd.Flags().Bool("no-builds", false, "Conda: disable the build string in spec (alias)")
	cmd.Flags().Bool("channel-subdir", false, "Conda: enable channel/subdir in spec")
	cmd.Flags().Bool("from-history", false, "Conda: only explicitly installed packages")
}
