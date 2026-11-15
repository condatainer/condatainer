package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/shell"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	shellName  string
	shellAlias string
)

var shellCmd = &cobra.Command{
	Use:          "shell",
	Short:        "Set up condatainer in the shell",
	Long:         "Set up condatainer in the shell: PATH, tab completion, and the mm command.",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(shellCmd)
	shellCmd.AddCommand(shellInitCmd, shellRemoveCmd, newCompletionCmd(false))
	rootCmd.AddCommand(newCompletionCmd(true))
}

// loadHomeOverride reads the config layers, which hold home_override.
func loadHomeOverride() {
	if exe, err := os.Executable(); err == nil {
		config.LoadDefaults(exe)
	}
	if err := config.LoadLayers(); err != nil {
		utils.PrintDebug("Error reading config file: %v", err)
	}
}

func printShellResult(res shell.Result) {
	for _, c := range res.Changes {
		switch c.Action {
		case shell.Created:
			utils.PrintMessage("Created %s", c.Path)
		case shell.Appended:
			utils.PrintMessage("Added the setup to %s", c.Path)
		case shell.Updated:
			utils.PrintMessage("Updated the setup in %s", c.Path)
		case shell.Unchanged:
			utils.PrintMessage("The setup in %s is up to date", c.Path)
		case shell.Removed:
			utils.PrintMessage("Removed the setup from %s", c.Path)
		case shell.Absent:
			utils.PrintMessage("No setup in %s", c.Path)
		}
	}
	for _, w := range res.Warnings {
		utils.PrintWarning("%s", w)
	}
}

// shellFromPath maps an executable path to a supported shell name, matching on
// the base name so a path like /home/zsh-fan/bin/foo does not match.
// Returns "" for anything else.
func shellFromPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(base, "fish"):
		return "fish"
	case strings.Contains(base, "zsh"):
		return "zsh"
	case strings.Contains(base, "bash"):
		return "bash"
	}
	return ""
}

// detectCompletionShell detects which shell is requesting completions from the
// parent process via /proc, falling back to $SHELL only when /proc is unreadable.
//
// An unrecognized parent is reported as unsupported rather than guessed at: the
// output is meant to be sourced, so emitting the wrong shell's script turns a
// clear error into a screenful of syntax errors in the user's login.
func detectCompletionShell() (string, error) {
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", os.Getppid())); err == nil {
		if shell := shellFromPath(exe); shell != "" {
			return shell, nil
		}
		return "", fmt.Errorf("no supported shell detected (parent process: %s)", filepath.Base(exe))
	}
	if shell := shellFromPath(os.Getenv("SHELL")); shell != "" {
		return shell, nil
	}
	return "", fmt.Errorf("could not detect the current shell")
}
