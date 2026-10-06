package cmd

import (
	"context"
	"fmt"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	initLayer string // user, app-root, extra-root, or a custom path
	getLayer  string // target layer for config get (single-layer read)
	setLayer  string // target layer for config set/append/prepend/remove
)

func isArrayKey(key string) bool {
	k, ok := settings.Lookup(key)
	return ok && k.IsList()
}

// refuseSourcesKey exits when key is `sources`, which `config source` manages.
func refuseSourcesKey(key string) {
	if key == "sources" {
		ExitWithError("sources are managed with `condatainer config source add|move|remove`")
	}
}

// modifyArrayConfig applies modify to key's list in the writable config file
// and returns that file's layer.
func modifyArrayConfig(key string, modify func([]string) []string) (string, error) {
	refuseSourcesKey(key)
	if !isArrayKey(key) {
		var arrayKeys []string
		for k, isArr := range knownConfigKeys() {
			if isArr {
				arrayKeys = append(arrayKeys, k)
			}
		}
		sort.Strings(arrayKeys)
		return "", fmt.Errorf("'%s' is not an array config key; array keys: %s",
			key, strings.Join(arrayKeys, ", "))
	}
	configPath, layer, fellBackFrom, err := config.ResolveWritableConfigPathVerbose(setLayer)
	if err != nil {
		return "", err
	}
	if fellBackFrom != "" {
		utils.PrintWarning("%s config is read-only; saving to your user config instead (applies only to you).", fellBackFrom)
	}
	current := config.ReadConfigSliceKey(configPath, key)
	updated := modify(current)
	if len(updated) == 0 {
		return layer, config.DeleteConfigKey(configPath, key)
	}
	return layer, config.UpdateConfigKey(configPath, key, updated)
}

func arrayKeyCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		var keys []string
		for k, isArr := range knownConfigKeys() {
			if isArr {
				keys = append(keys, k)
			}
		}
		return keys, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		switch args[0] {
		}
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func arrayRemoveValueCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	loc, _ := cmd.Flags().GetString("layer")
	if len(args) == 0 {
		// Suggest only keys that are actually set in the target config file.
		if configPath, _, err := config.ResolveWritableConfigPath(loc); err == nil {
			v := config.ReadConfigAllKeys(configPath)
			if len(v) > 0 {
				sort.Strings(v)
				return v, cobra.ShellCompDirectiveNoFileComp
			}
		}
		// Fallback: all known keys
		var keys []string
		for k := range knownConfigKeys() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 && isArrayKey(args[0]) {
		// Suggest values present in the target config file for this array key.
		if configPath, _, err := config.ResolveWritableConfigPath(loc); err == nil {
			return config.ReadConfigSliceKey(configPath, args[0]), cobra.ShellCompDirectiveNoFileComp
		}
		return effectiveList(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// getConfigEnvVars returns a sorted list of CNT_CONFIG_* environment variables for all known config keys.
func getConfigEnvVars() []string {
	known := knownConfigKeys()
	vars := make([]string, 0, len(known))
	for key := range known {
		vars = append(vars, settings.EnvName(key))
	}
	sort.Strings(vars)
	return vars
}

// scalarConfigKeys returns all non-array config keys.
func scalarConfigKeys() []string {
	var out []string
	for k, isArr := range knownConfigKeys() {
		if !isArr {
			out = append(out, k)
		}
	}
	return out
}

// configSetKeysCompletion returns only scalar (non-array) config keys for `config set`.
func configSetKeysCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return scalarConfigKeys(), cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		return configValueCompletion(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// configKeysCompletion returns config keys for shell completion
func configKeysCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		// First arg: complete all config keys
		var keys []string
		for k := range knownConfigKeys() {
			keys = append(keys, k)
		}
		return keys, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		// Second arg: complete values based on the key
		return configValueCompletion(args[0]), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// recipeExists reports whether a module name resolves in any configured source.
func recipeExists(ctx context.Context, name string) bool {
	cat, err := config.OpenCatalog(ctx)
	if err != nil {
		return false
	}
	_, found, err := cat.Lookup(ctx, name)
	return err == nil && found
}

// configValueCompletion returns suggested values for a config key
func configValueCompletion(key string) []string {
	if k, ok := settings.Lookup(key); ok {
		return k.Suggest()
	}
	return nil
}

// configLayersHelp documents the -l/--layer values. Shared by every config
// subcommand that accepts --layer so the list stays consistent, not copy-pasted.
const configLayersHelp = `Config file layers (-l, --layer):
  u, user        ~/.config/condatainer/config.yaml (standard/home install)
  r, app-root    <install-dir>/config.yaml         (dedicated install, or CNT_ROOT)
  e, extra-root  $CNT_EXTRA_ROOT/config.yaml       (group, needs CNT_EXTRA_ROOT)`

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage condatainer configuration",
	Long: `Manage condatainer configuration.

Setting priority (highest to lowest):
  1. Command-line flags
  2. Environment variables (CNT_CONFIG_*)
  3. User config file (~/.config/condatainer/config.yaml)
  4. Extra-root config ($CNT_EXTRA_ROOT/config.yaml, group layer)
  5. App-root config (<install-dir>/config.yaml, in dedicated folder or CNT_ROOT set)
  6. Defaults

Data directory priority — reads go nearest-first, writes furthest-first:

  read (first match wins)          write (first writable)
  1. Scratch ($SCRATCH)            1. Extra-root ($CNT_EXTRA_ROOT, group layer)
  2. User XDG (~/.local/share)     2. App-root (auto-detected or CNT_ROOT)
  3. Extra-root ($CNT_EXTRA_ROOT)  3. Scratch ($SCRATCH/condatainer)
  4. App-root (auto-detected)      4. User XDG (~/.local/share/condatainer)

- A build goes to the first writable directory in the write column.
- Build into your own directory to have your version win for you.`,
}

var showOrigin bool

var configListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"show", "ls"},
	Args:    cobra.NoArgs,
	Short:   "List current configuration",
	Long: `List the current settings and where each comes from.

- The config files searched, in priority order.
- Every value.
- Environment variable overrides.
- With --origin, each value is tagged with the layer or environment variable that sets it, or [default].
- Use 'condatainer config paths' for the data directories.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Show config file search paths
		fmt.Println(utils.StyleTitle("Config File Search Paths:"))
		searchPaths := config.GetConfigSearchPaths()
		foundActive := false
		for i, sp := range searchPaths {
			status := ""
			if sp.InUse {
				foundActive = true
			} else if !sp.Exists {
				status = " " + utils.StyleWarning("(not found)")
			}
			fmt.Printf("  %d. [%s] %s%s\n", i+1, sp.Type, sp.Path, status)
		}
		if !foundActive {
			fmt.Printf("  %s (use `condatainer config init` to create)\n", utils.StyleWarning("No config file found"))
		}
		fmt.Println()

		// Remote sources
		fmt.Println(utils.StyleTitle("Recipe Sources:"))
		if len(config.Global.Sources) > 0 {
			fmt.Printf("  sources:\n")
			for _, src := range config.Global.Sources {
				fmt.Printf("    - %s: %s\n", src.Name, src.Base)
			}
		} else {
			fmt.Printf("  sources: %s\n", "none configured")
		}
		fmt.Println()

		fmt.Println(utils.StyleTitle("Options:"))
		top := settings.TopLevel()
		width := labelWidth(top, "")
		for _, k := range top {
			if k.IsList() {
				printListSetting(k.Name, width)
			} else {
				printSetting(k.Name, "", width)
			}
			if k.Name == "home_override" {
				printDistroNote(cmd.Context(), width)
			}
		}
		fmt.Println()

		fmt.Printf("%s %s\n", utils.StyleTitle("Helper Configuration:"), "helper.*")
		printSection("helper")
		fmt.Println()

		fmt.Printf("%s %s\n", utils.StyleTitle("Build Configuration:"), "build.*")
		printSection("build")
		fmt.Println()

		fmt.Printf("%s %s\n", utils.StyleTitle("Scheduler Configuration:"), "scheduler.*")
		printSection("scheduler")
		fmt.Println()

		// Show environment variable overrides
		fmt.Println(utils.StyleTitle("Environment Variable Overrides:"))
		envVars := getConfigEnvVars()
		hasEnvOverrides := false
		// Special vars not derived from config keys (system → group → personal)
		for _, envVar := range []string{"CNT_ROOT", "CNT_LIBEXEC", "CNT_EXTRA_ROOT", "SCRATCH"} {
			if val := os.Getenv(envVar); val != "" {
				fmt.Printf("  %s=%s\n", envVar, val)
				hasEnvOverrides = true
			}
		}
		for _, envVar := range envVars {
			if val := os.Getenv(envVar); val != "" {
				fmt.Printf("  %s=%s\n", envVar, val)
				hasEnvOverrides = true
			}
		}
		if !hasEnvOverrides {
			fmt.Printf("  %s\n", "none")
		}
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get [flags] <key>",
	Short: "Get a configuration value",
	Long: `Get a configuration value.

- Without -l, it returns the effective value (environment and all config layers).
- With -l, it reads only that layer's config file.

` + configLayersHelp,
	Example: `  condatainer config get build.system_apptainer
  condatainer config get build.ncpus
  condatainer config get sources
  condatainer config get sources -l app-root`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: configKeysCompletion,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		if key == "sources" {
			printSourcesKey()
			return
		}

		// Layer-specific read
		if getLayer != "" {
			configPath, _, err := config.ResolveReadableConfigPath(getLayer)
			if err != nil {
				ExitWithError("Invalid layer: %v", err)
			}
			if isArrayKey(key) {
				for _, v := range config.ReadConfigSliceKey(configPath, key) {
					fmt.Println(v)
				}
			} else {
				fmt.Println(config.ReadConfigKey(configPath, key))
			}
			return
		}

		// Merged effective value: env > layers in priority order > default
		if res, ok := settings.Resolve(key); ok {
			printEffective(res)
			return
		}
		if k, a, ok := settings.LookupAlias(key); ok {
			utils.PrintNote("%s", a.RenameMessage())
			res, _ := settings.Resolve(k.Name)
			printEffective(res)
			return
		}
		if r, ok := settings.LookupRemoved(key); ok {
			ExitWithError("%s", r.Text())
		}
		ExitWithError("Unknown config key: %s", key)
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set [flags] <key> <value>",
	Short: "Set a configuration value",
	Long: `Set a configuration value and save it to a config file.

Time format (for build.time):
  - Go style:  2h, 30m, 1h30m, 90s
  - HPC style: 02:00:00, 2:30:00, 1:30 (HH:MM:SS or HH:MM)

` + configLayersHelp,
	Example: `  condatainer config set build.system_apptainer /usr/bin/apptainer
  condatainer config set build.ncpus 8
  condatainer config set build.time 02:00:00
  condatainer config set scheduler.submit_job false`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: configSetKeysCompletion,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		value := args[1]
		if strings.TrimSpace(value) == "" {
			ExitWithError("value cannot be empty")
		}

		refuseSourcesKey(key)
		if k, ok := settings.Lookup(key); ok {
			value = parseRegistered(k, value)
		} else if refuseOldName(key) {
			os.Exit(ExitCodeError)
		} else {
			utils.PrintError("Unknown config key: %s", key)
			utils.PrintHint("`condatainer config help` lists the keys")
			os.Exit(ExitCodeError)
		}

		// A default distro must name a recipe that some source actually provides.
		if key == "default_distro" {
			if !recipeExists(cmd.Context(), value+"/base") {
				utils.PrintError("No recipe %s/base in any configured source", value)
				os.Exit(ExitCodeError)
			}
		}

		// Array keys require append/prepend/remove subcommands
		if isArrayKey(key) {
			utils.PrintError("'%s' is an array setting. Use append/prepend/remove subcommands.", key)
			os.Exit(ExitCodeError)
		}

		configPath, layerType, fellBackFrom, err := config.ResolveWritableConfigPathVerbose(setLayer)
		if err != nil {
			ExitWithError("%v", err)
		}
		if fellBackFrom != "" {
			utils.PrintWarning("%s config is read-only; saving to your user config instead (applies only to you).", fellBackFrom)
		}
		if err := config.UpdateConfigKey(configPath, key, value); err != nil {
			ExitWithError("Failed to save config: %v", err)
		}

		utils.PrintMessage("Set %s = %s (%s layer)", key, value, utils.StyleName(layerType))
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Args:  cobra.NoArgs,
	Short: "Create a config file with defaults",
	Long: `Create a configuration file with default values and auto-detected settings.

` + configLayersHelp + `

Without -l, the layer follows the install location:
  - Executable in a dedicated folder (e.g. /apps/condatainer/bin/): that folder's parent.
  - Otherwise: the user config directory.`,
	Example: `  condatainer config init                # Layer chosen from the install location
  condatainer config init -l app-root    # A specific layer
  condatainer config init -l r           # Same, using the shortcut`,
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
			if layerType == "app-root" {
				if rootDir := filepath.Dir(configPath); !utils.CanWriteToDir(rootDir) {
					ExitWithError(
						"App-root config directory is read-only: %s\n"+
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
				layerType = "app-root"
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
		layerOrder := []string{"user", "extra-root", "app-root"}
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
		detectedApptainerBin, detectedSchedulerBin := detected["build.system_apptainer"], detected["scheduler.bin"]
		if detectedApptainerBin == "" {
			utils.PrintWarning("Neither 'apptainer' nor 'singularity' binary found (checked PATH and 'module avail'), so os overlays cannot be built.")
		}

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
		if detectedApptainerBin != "" {
			fmt.Printf("  Apptainer: %s\n", detectedApptainerBin)
		} else {
			fmt.Printf("  Apptainer: %s\n", utils.StyleWarning("not found"))
		}
		if detectedSchedulerBin != "" {
			fmt.Printf("  Scheduler: %s (%s)\n", detectedSchedulerBin, scheduler.TypeFromBin(detectedSchedulerBin))
		} else {
			fmt.Printf("  Scheduler: %s\n", utils.StyleWarning("not found"))
		}
	},
}

var configPathsCmd = &cobra.Command{
	Use:   "paths",
	Args:  cobra.NoArgs,
	Short: "Show data search paths",
	Long: `Show the search paths for images and helper scripts.

Reads check these nearest-first, and the first match wins:
  1. Scratch ($SCRATCH/condatainer)
  2. User XDG directory (~/.local/share/condatainer)
  3. Extra-root ($CNT_EXTRA_ROOT, group layer)
  4. App-root (auto-detected or $CNT_ROOT)

Writes use the first writable directory in the reverse order:
extra-root, app-root, scratch, user.`,
	Run: func(cmd *cobra.Command, args []string) {
		// pathStatus returns inline status tags for a search-path entry.
		// writeTarget is the resolved writable directory for this section (empty = not applicable).
		pathStatus := func(dir, writeTarget string) string {
			if !config.DirExists(dir) {
				if writeTarget != "" && dir == writeTarget {
					return " " + utils.StyleSuccess("(target, created on first write)")
				}
				return " " + utils.StyleWarning("(not found)")
			}
			var tags string
			if utils.CanWriteToDir(dir) {
				if writeTarget != "" && dir == writeTarget {
					tags = " " + utils.StyleSuccess("(writable, target)")
				} else {
					tags = " " + utils.StyleSuccess("(writable)")
				}
			} else {
				tags = " " + utils.StyleWarning("(read-only)")
			}
			return tags
		}

		imagesWritable := config.PeekWritableImagesDir()
		helperWritable := config.PeekWritableHelperScriptsDir()
		cacheWritable := config.PeekWritableCacheDir()

		// withLayer appends the data layer a directory belongs to, matching the
		// tags in `list` output and the -l/--layer values commands accept.
		withLayer := func(dir string) string {
			return dir + " " + utils.StyleDim("("+string(config.ClassifyDataDir(dir))+")")
		}

		// Recipe sources, in resolution order (first match wins)
		fmt.Println(utils.StyleTitle("Sources:"))
		if len(config.Global.Sources) == 0 {
			fmt.Println("  (none configured)")
		}
		for i, src := range config.Global.Sources {
			// A source is read-only, so writability says nothing; only whether a
			// local one is actually there.
			status := ""
			if !strings.HasPrefix(src.Base, "http") && !config.DirExists(src.Base) {
				status = " " + utils.StyleWarning("(not found)")
			}
			fmt.Printf("  %d. %s %s%s\n", i+1, src.Name, src.Base, status)
		}
		fmt.Println()

		// Images
		fmt.Println(utils.StyleTitle("Images:"))
		for i, dir := range config.GetImageSearchPaths() {
			fmt.Printf("  %d. %s%s\n", i+1, withLayer(dir), pathStatus(dir, imagesWritable))
		}
		fmt.Println()

		// Helper scripts
		fmt.Println(utils.StyleTitle("Helper Scripts:"))
		for i, dir := range config.GetHelperScriptSearchPaths() {
			fmt.Printf("  %d. %s%s\n", i+1, withLayer(dir), pathStatus(dir, helperWritable))
		}
		fmt.Println()

		// Cache
		fmt.Println(utils.StyleTitle("Cache:"))
		for i, dir := range config.GetCacheSearchPaths() {
			fmt.Printf("  %d. %s%s\n", i+1, dir, pathStatus(dir, cacheWritable))
		}
		fmt.Println()

		// Config file search paths
		fmt.Println(utils.StyleTitle("Config Files:"))
		for _, cp := range config.GetConfigSearchPaths() {
			var status string
			if !cp.Exists {
				status = " " + utils.StyleWarning("(not found)")
			}
			fmt.Printf("  %-12s %s%s\n", cp.Type+":", cp.Path, status)
		}
		fmt.Println()

		// Directory info
		fmt.Println(utils.StyleTitle("Directory Locations:"))
		if rootDir := config.GetRootDir(); rootDir != "" {
			fmt.Printf("  App-root: %s\n", rootDir)
		} else {
			fmt.Printf("  App-root: %s\n", utils.StyleWarning("not detected (set CNT_ROOT to override)"))
		}
		if scratchDir := config.GetScratchDataDir(); scratchDir != "" {
			fmt.Printf("  Scratch:  %s\n", scratchDir)
		} else {
			fmt.Printf("  Scratch:  %s\n", utils.StyleWarning("$SCRATCH not set"))
		}
		if userDir := config.GetUserDataDir(); userDir != "" {
			fmt.Printf("  User XDG: %s\n", userDir)
		}
		if libexecDir := config.GetLibexecDir(); libexecDir != "" {
			fmt.Printf("  Libexec:  %s%s\n", libexecDir, pathStatus(libexecDir, ""))
		}
	},
}

var configAppendCmd = &cobra.Command{
	Use:   "append [flags] <key> <value>",
	Short: "Append a value to an array config key",
	Long: `Append a value to an array config key (lowest search priority).

` + configLayersHelp,
	Example:           `  condatainer config append bind /scratch`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: arrayKeyCompletion,
	SilenceUsage:      true,
	Run: func(cmd *cobra.Command, args []string) {
		key, value := args[0], args[1]
		if strings.TrimSpace(value) == "" {
			ExitWithError("value cannot be empty")
		}
		moved := false
		layer, err := modifyArrayConfig(key, func(cur []string) []string {
			out := make([]string, 0, len(cur))
			for _, v := range cur {
				if v == value {
					moved = true
					continue
				}
				out = append(out, v)
			}
			return append(out, value)
		})
		if err != nil {
			ExitWithError("%v", err)
		}
		if moved {
			utils.PrintMessage("Moved %s to the end of %s (%s layer)", value, key, utils.StyleName(layer))
		} else {
			utils.PrintMessage("Appended %s to %s (%s layer)", value, key, utils.StyleName(layer))
		}
	},
}

var configPrependCmd = &cobra.Command{
	Use:   "prepend [flags] <key> <value>",
	Short: "Prepend a value to an array config key (highest priority)",
	Long: `Prepend a value to an array config key (highest search priority).

` + configLayersHelp,
	Example:           `  condatainer config prepend channels bioconda`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: arrayKeyCompletion,
	SilenceUsage:      true,
	Run: func(cmd *cobra.Command, args []string) {
		key, value := args[0], args[1]
		if strings.TrimSpace(value) == "" {
			ExitWithError("value cannot be empty")
		}
		moved := false
		layer, err := modifyArrayConfig(key, func(cur []string) []string {
			out := make([]string, 0, len(cur))
			for _, v := range cur {
				if v == value {
					moved = true
					continue
				}
				out = append(out, v)
			}
			return append([]string{value}, out...)
		})
		if err != nil {
			ExitWithError("%v", err)
		}
		if moved {
			utils.PrintMessage("Moved %s to the front of %s (%s layer)", value, key, utils.StyleName(layer))
		} else {
			utils.PrintMessage("Prepended %s to %s (%s layer)", value, key, utils.StyleName(layer))
		}
	},
}

var configRemoveCmd = &cobra.Command{
	Use:     "remove [flags] <key> [value]",
	Aliases: []string{"rm"},
	Short:   "Remove a config key or a value from an array key",
	Long: `Remove a config key entirely, or one value from an array config key.

` + configLayersHelp,
	Example: `  condatainer config remove build.system_apptainer
  condatainer config remove bind /scratch`,
	Args:              cobra.RangeArgs(1, 2),
	ValidArgsFunction: arrayRemoveValueCompletion,
	SilenceUsage:      true,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		refuseSourcesKey(key)
		if len(args) == 1 {
			// Scalar removal: unset the key entirely
			if isArrayKey(key) {
				ExitWithError("'%s' is an array key; specify a value to remove: config remove %s <value>", key, key)
			}
			configPath, layer, err := config.ResolveWritableConfigPath(setLayer)
			if err != nil {
				ExitWithError("%v", err)
			}
			if config.ReadConfigKey(configPath, key) == "" {
				utils.PrintWarning("%s is not set in the %s layer", key, utils.StyleName(layer))
				return
			}
			if err := config.DeleteConfigKey(configPath, key); err != nil {
				ExitWithError("%v", err)
			}
			utils.PrintMessage("Removed %s (%s layer)", key, utils.StyleName(layer))
			return
		}
		// Array removal
		value := args[1]
		removed := false
		layer, err := modifyArrayConfig(key, func(cur []string) []string {
			var out []string
			for _, v := range cur {
				if v == value {
					removed = true
					continue
				}
				out = append(out, v)
			}
			return out
		})
		if err != nil {
			ExitWithError("%v", err)
		}
		if removed {
			utils.PrintMessage("Removed %s from %s (%s layer)", value, key, utils.StyleName(layer))
		} else {
			utils.PrintWarning("%s not found in %s (%s layer)", value, key, utils.StyleName(layer))
		}
	},
}

func init() {
	// Add flags
	configInitCmd.Flags().StringVarP(&initLayer, "layer", "l", "", "Config layer to create in: u/user, e/extra-root, r/app-root")
	configGetCmd.Flags().StringVarP(&getLayer, "layer", "l", "", "Read only this config layer: u/user, e/extra-root, r/app-root")
	configSetCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Config layer to write: u/user, e/extra-root, r/app-root")
	configAppendCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Config layer to write: u/user, e/extra-root, r/app-root")
	configPrependCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Config layer to write: u/user, e/extra-root, r/app-root")
	configRemoveCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Config layer to write: u/user, e/extra-root, r/app-root")

	// Add subcommands
	configListCmd.Flags().BoolVar(&showOrigin, "origin", false, "Tag each value with the layer or environment variable that sets it")
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configAppendCmd)
	configCmd.AddCommand(configPrependCmd)
	configCmd.AddCommand(configRemoveCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configPathsCmd)
	configCmd.AddCommand(configHelpCmd)
	configCmd.AddCommand(configCheckCmd)

	// Add to root command
	rootCmd.AddCommand(configCmd)
}
