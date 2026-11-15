package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	ui "github.com/condatainer/condatainer/cmd/internal/ui"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var helperCmd = &cobra.Command{
	Use:   "helper",
	Short: "Run web apps like RStudio on HPC",
	Long: `Run web apps like RStudio inside CondaTainer on HPC.

- It submits a job, starts the app, and opens an SSH tunnel to your browser.
- With no arguments, it shows the apps you have running.
- It cannot run inside a container or a scheduler job.`,
	Example: `  condatainer helper                       # Show running apps
  condatainer helper run code-server       # Start code-server
  condatainer helper run code-server -h    # Show its flags and defaults
  condatainer helper config code-server    # Show or save its defaults
  condatainer helper stop                  # Stop a running app
  condatainer helper avail                 # List the helpers the sources offer
  condatainer helper add code-server       # Add one
  condatainer helper upgrade               # Bring added helpers up to date`,
	SilenceUsage: true,
	RunE:         runHelper,
}

var (
	helperSources []string
	helperLayer   string
	helperForce   bool
)

func init() {
	rootCmd.AddCommand(helperCmd)
	helperCmd.AddCommand(helperRunCmd, helperStopCmd, helperConfigCmd, helperAvailCmd, helperAddCmd, helperRemoveCmd,
		helperUpgradeCmd, helperListCmd, helperPathCmd)
}

// sourceFlag adds --source to a command that reads the configured sources.
func sourceFlag(c *cobra.Command) {
	c.Flags().StringArrayVarP(&helperSources, "source", "s", nil, "Use only this configured source, in flag order (repeatable)")
	c.RegisterFlagCompletionFunc("source", sourceHandleCompletion) //nolint:errcheck
}

// completeHelperScripts provides shell completion for helper script names
func completeHelperScripts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Only complete the first argument (script name), then return to default file completion
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}

	// Collect scripts from all search paths (user → system)
	seen := make(map[string]bool)
	var scripts []string

	for _, dir := range config.GetHelperScriptSearchPaths() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			name := entry.Name()
			// Skip hidden files and directories
			if !entry.IsDir() && !strings.HasPrefix(name, ".") {
				if !seen[name] {
					seen[name] = true
					scripts = append(scripts, name)
				}
			}
		}
	}

	return scripts, cobra.ShellCompDirectiveNoFileComp
}

// refuseInsideJob exits when a helper command runs inside a container or a scheduler job.
func refuseInsideJob(cmd *cobra.Command) {
	if config.IsInsideContainer() {
		cmd.SilenceUsage = true
		ExitWithError("helper commands are not available inside a container")
	}
	if scheduler.IsInsideJob() {
		cmd.SilenceUsage = true
		ExitWithError("helper commands are not available inside a scheduler job")
	}
}

func runHelper(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if len(args) > 0 {
		ExitWithError("unknown command %q: start a helper with `condatainer helper run %s`", args[0], args[0])
	}
	refuseInsideJob(cmd)
	helper.EnsureServer()
	return showHelperStatus("")
}

// showHelperStatus lists running helpers (optionally filtered by name) from JSONL history.
func showHelperStatus(name string) error {
	running, err := helper.RunningHelpers(name)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		if name != "" {
			fmt.Printf("No %s helpers running. Use `condatainer helper run %s` to start.\n", name, name)
		} else {
			fmt.Println("No helpers running. Use `condatainer helper run <name>` to start.")
		}
		return nil
	}
	serverPort := config.GetRunningServerPort()
	for _, r := range running {
		url := r.AccessURL(serverPort)
		age := ui.FormatAge(time.Since(r.StartedAt))
		res := ui.FormatRunResources(r)
		cwd := r.CWD
		if cwd == "" {
			cwd = "(no cwd)"
		}
		tag, verb := ui.RunStatusTag(r)
		runnerTag := ""
		if r.Runner != "" {
			runnerTag = " [" + r.Runner + "]"
		}
		fmt.Printf("  %-14s  %-14s  %-40s  %s %s %s ago%s\n", r.Name, res, cwd, tag, verb, age, runnerTag)
		if url != "" {
			fmt.Printf("    %s\n", utils.StyleDim("→ "+url))
		}
	}
	return nil
}

// helperLayerFlag reads --layer, or exits on a name that is not a layer.
func helperLayerFlag(cmd *cobra.Command) config.DataLayer {
	if helperLayer == "" {
		return ""
	}
	layer, err := config.ParseDataLayer(helperLayer)
	if err != nil {
		cmd.SilenceUsage = true
		ExitWithError("%v", err)
	}
	return layer
}
