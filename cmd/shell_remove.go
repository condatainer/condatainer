package cmd

import (
	"github.com/condatainer/condatainer/internal/shell"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var shellRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove condatainer's setup from the shell's rc file",
	Long: `Remove condatainer's setup from the shell's rc file.

- With home_override set, the rc file under the replacement home is cleaned too.
- It is refused inside a container. Run it on the host.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runShellRemove,
}

func init() {
	shellRemoveCmd.Flags().StringVar(&shellName, "shell", "",
		"bash, zsh, or fish; default: all of them")
}

func runShellRemove(cmd *cobra.Command, _ []string) error {
	all := shellName == ""
	shells := shell.All
	if !all {
		sh, err := shell.Parse(shellName)
		if err != nil {
			return err
		}
		shells = []shell.Shell{sh}
	}
	loadHomeOverride()
	res, err := shell.Remove(shells)
	if all {
		res = withoutAbsent(res)
		if err == nil && len(res.Changes) == 0 {
			utils.PrintMessage("No setup found in any shell's rc file")
		}
	}
	printShellResult(res)
	return err
}

// withoutAbsent drops the files that held no setup, so removing from every shell lists only what changed.
func withoutAbsent(res shell.Result) shell.Result {
	kept := res.Changes[:0:0]
	for _, c := range res.Changes {
		if c.Action != shell.Absent {
			kept = append(kept, c)
		}
	}
	res.Changes = kept
	return res
}
