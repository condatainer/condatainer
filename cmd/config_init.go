package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configInitCmd = &cobra.Command{
	Use:   "init",
	Args:  cobra.NoArgs,
	Short: "Create a config file with defaults",
	Long: `Create a configuration file with default values and auto-detected settings.

` + configLayersHelp + `

Without -l, the layer is cnt-root if it is writable, otherwise user.`,
	Example: `  condatainer config init
  condatainer config init -l cnt-root  # A specific layer
  condatainer config init -l r         # Same, using the shortcut`,
	Run: func(cmd *cobra.Command, args []string) {
		// Before anything is asked or resolved, so a container never gets as far as
		// an overwrite prompt.
		if config.IsInsideContainer() {
			ExitWithError("Cannot initialize config inside a container. Please run this command on the host system.")
		}

		var configPath string
		var err error
		var layerType string

		// Determine config path based on --layer flag or smart default
		if initLayer != "" {
			// User specified a layer
			configPath, err = config.GetConfigPathByLayer(initLayer)
			if err != nil {
				ExitWithError("Invalid layer: %v", err)
			}
			layerType = config.NormalizeConfigLayer(initLayer)
			// Fail early with an actionable error if the root dir is read-only
			if layerType == "cnt-root" {
				if rootDir := filepath.Dir(configPath); !utils.CanWriteToDir(rootDir) {
					ExitWithError(
						"cnt-root config directory is read-only: %s\n"+
							"Run as a privileged user, or use '-l user' to create a personal config instead.",
						rootDir,
					)
				}
			}
		} else {
			// Smart default: root if in dedicated installation AND writable, otherwise user
			rootPath := config.GetRootConfigPath()
			if rootPath != "" && utils.CanWriteToDir(filepath.Dir(rootPath)) {
				configPath = rootPath
				layerType = "cnt-root"
			} else {
				if rootPath != "" {
					// Standalone layout detected but dir is read-only — fall back gracefully
					utils.PrintNote("Standalone layout detected but directory is read-only; using user config instead.")
				}
				configPath, err = config.GetUserConfigPath()
				if err != nil {
					ExitWithError("Failed to get config path: %v", err)
				}
				layerType = "user"
			}
		}

		// Check if config already exists
		if _, err := os.Stat(configPath); err == nil {
			utils.PrintWarning("Config file already exists: %s", configPath)
			if !utils.ShouldAnswerYes() && !utils.Confirm(cmd.Context(), os.Stdout, "Overwrite? [y/N]: ") {
				return
			}
		}

		// lowerLayersFor returns loaded config layers that are lower priority than loc.
		// Keys already set in these layers will be skipped when saving.
		layerOrder := []string{"user", "extra-root", "cnt-root"}
		lowerLayersFor := func(loc string) []*config.Layer {
			cutIdx := slices.Index(layerOrder, loc) + 1
			var result []*config.Layer
			for _, l := range config.GetConfigLayers() {
				if slices.Index(layerOrder, l.Type) >= cutIdx {
					result = append(result, l)
				}
			}
			return result
		}

		// Write the keys that can be detected from this host. Everything else keeps its default until set.
		detected := map[string]string{}
		for _, k := range settings.Keys() {
			if v := k.Detect(); k.CanDetect() && v != "" {
				detected[k.Name] = v
			}
		}
		detectedSchedulerBin := detected["scheduler.bin"]

		if err := config.SaveDetectedConfigTo(configPath, detected, lowerLayersFor(layerType)); err != nil {
			ExitWithError("Failed to save config: %v", err)
		}

		utils.PrintMessage("Created %s (%s layer)", configPath, utils.StyleName(layerType))

		// Record the default distro now, from whichever source recommends one.
		// It is written once and never revised, so the container root stays put.
		if cat, err := config.OpenCatalog(cmd.Context()); err == nil {
			if distro := config.EnsureDefaultDistro(cat); distro != "" {
				fmt.Printf("  Distro:   %s (from %s)\n", distro, "default_distro")
			}
		}

		// Show what was detected
		fmt.Println()
		fmt.Println(utils.StyleTitle("Detected settings:"))
		if detectedSchedulerBin != "" {
			fmt.Printf("  Scheduler: %s (%s)\n", detectedSchedulerBin, scheduler.TypeFromBin(detectedSchedulerBin))
		} else {
			fmt.Printf("  Scheduler: %s\n", utils.StyleWarning("not found"))
		}
	},
}

func init() {
	configInitCmd.Flags().StringVarP(&initLayer, "layer", "l", "", "Layer to create in: "+layerChoices)
}
