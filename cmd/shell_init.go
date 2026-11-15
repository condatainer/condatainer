package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/shell"
	"github.com/spf13/cobra"
)

var shellInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Write condatainer's setup into the shell's rc file",
	Long: `Write condatainer's setup into the shell's rc file.

- It sets PATH, loads tab completion, and defines mm inside a container.
- With home_override set, the rc file under the replacement home is set up too.
- It is refused inside a container. Run it on the host.`,
	Example: `  condatainer shell init
  condatainer shell init --shell zsh --alias cnt`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runShellInit,
}

func init() {
	shellInitCmd.Flags().StringVar(&shellName, "shell", "",
		"bash, zsh, or fish; default: the current shell. Bash is always set up too")
	shellInitCmd.Flags().StringVar(&shellAlias, "alias", "", "Define a short alias (e.g. cnt) and complete it too")
}

// setupShell resolves --shell, else the shell running the command.
func setupShell() (shell.Shell, error) {
	name := shellName
	if name == "" {
		detected, err := detectCompletionShell()
		if err != nil {
			return "", fmt.Errorf("%w; specify it with --shell bash|zsh|fish", err)
		}
		name = detected
	}
	return shell.Parse(name)
}

func runShellInit(cmd *cobra.Command, _ []string) error {
	sh, err := setupShell()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find the condatainer binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	loadHomeOverride()
	res, err := shell.Init(shell.Options{Shell: sh, Alias: shellAlias, Exe: exe})
	printShellResult(res)
	return err
}
