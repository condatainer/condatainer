package cmd

import (
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var configPathsCmd = &cobra.Command{
	Use:   "paths",
	Args:  cobra.NoArgs,
	Short: "Show data search paths",
	Long: `Show the search paths for overlays and helper scripts.

` + dataDirsHelp + `

` + cntRootHelp,
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
			fmt.Printf("  cnt-root: %s\n", rootDir)
		} else {
			fmt.Printf("  cnt-root: %s\n", utils.StyleWarning("not detected (set CNT_ROOT to override)"))
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
