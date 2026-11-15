package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	updateLibexec bool
	updateForce   bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Refresh recipe and helper caches, or update the toolchain",
	Long: `Refresh the recipe and helper script caches, or update the toolchain.

- With no flags, both caches are refreshed.

--libexec updates the toolchain instead.
  apptainer: Apptainer and the tools it and CondaTainer use.
  micromamba: the standalone micromamba binary.
  With no name, both are installed or updated.
  Writes to $CNT_ROOT/libexec, or $CNT_LIBEXEC when set.`,
	Example: `  condatainer update                          # Refresh recipe and helper caches
  # Install or update apptainer and micromamba
  condatainer update --libexec
  condatainer update --libexec apptainer      # Install or update apptainer only
  # A specific version
  condatainer update --libexec apptainer@1.5.4
  condatainer update --libexec apptainer@1.5  # The newest 1.5.x`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && !updateLibexec {
			return fmt.Errorf("tools can only be named with --libexec")
		}
		if updateForce && !updateLibexec {
			return fmt.Errorf("--force only works with --libexec")
		}
		return nil
	},
	SilenceUsage: true,
	RunE:         runUpdate,
}

// errLibexecInContainer is why --libexec does nothing inside a container.
var errLibexecInContainer = errors.New("--libexec must be run on the host, not inside a container")

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "With --libexec, reinstall a package that is already at the version to install")
	updateCmd.Flags().BoolVar(&updateLibexec, "libexec", false, "Install or update the toolchain; name apptainer or micromamba to do just that one")
}

// libexecHostError refuses --libexec inside a container.
func libexecHostError(insideContainer bool) error {
	if insideContainer {
		return errLibexecInContainer
	}
	return nil
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if updateLibexec {
		if err := libexecHostError(config.IsInsideContainer()); err != nil {
			return err
		}
	}

	if !updateLibexec {
		if err := config.RefreshCatalogCache(); err != nil {
			return fmt.Errorf("failed to clear the recipe cache: %w", err)
		}
		cat, err := config.OpenCatalog(cmd.Context())
		if err != nil {
			return fmt.Errorf("failed to open recipe sources: %w", err)
		}
		for _, src := range cat {
			utils.PrintMessage("Fetching recipes from %s (%s) ...", src.Name, src.Base)
		}
		entries := cat.Entries(cmd.Context())
		config.WarnUnreachableSources(cmd.Context(), cat)
		utils.PrintSuccess("%d recipes available.", len(entries))

		if _, err := helper.RefreshRemoteMetadata(cmd.Context(), true, os.Stdout); err != nil {
			utils.PrintWarning("Failed to update helper script metadata: %v", err)
		} else {
			utils.PrintSuccess("Helper script metadata updated.")
		}
	}

	if updateLibexec {
		utils.PrintMessage("Updating the self-provisioned toolchain...")
		if err := libexec.Update(cmd.Context(), updateForce, args...); err != nil {
			return fmt.Errorf("failed to update the toolchain: %w", err)
		}
		utils.PrintSuccess("Toolchain is up to date.")
		if versions, err := libexec.Versions(cmd.Context()); err == nil {
			for _, v := range versions {
				utils.PrintMessage("  %-10s %s", v.Name, v.Version)
			}
		}
	}

	return nil
}
