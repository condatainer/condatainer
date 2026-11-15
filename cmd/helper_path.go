package cmd

import (
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/spf13/cobra"
)

var helperPathCmd = &cobra.Command{
	Use:   "path [flags] [name]",
	Short: "Show the helper script directories",
	Long: `Show the helper directories in read order: user > extra-root > cnt-root.

- With a name, it prints the file that name resolves to.`,
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: completeHelperScripts,
	RunE:              runHelperPath,
}

func runHelperPath(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		scriptPath, err := config.FindHelperScript(args[0])
		if err != nil {
			cmd.SilenceUsage = true
			ExitWithError("helper script '%s' not found (searched: %s)",
				args[0], strings.Join(config.GetHelperScriptSearchPaths(), ", "))
		}
		fmt.Println(scriptPath)
		return nil
	}
	fmt.Println("Helper script search paths (priority order):")
	for i, dir := range config.GetHelperScriptSearchPaths() {
		exists := ""
		if !config.DirExists(dir) {
			exists = " (not found)"
		}
		fmt.Printf("  %d. %s%s\n", i+1, dir, exists)
	}
	if writableDir := config.PeekWritableHelperScriptsDir(); writableDir != "" {
		fmt.Printf("\nWritable directory: %s\n", writableDir)
	}
	return nil
}
