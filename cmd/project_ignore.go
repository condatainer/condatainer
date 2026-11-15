package cmd

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectIgnoreCmd() *cobra.Command {
	var remove, list bool
	cmd := &cobra.Command{
		Use:   "ignore [flags] <path>...",
		Short: "Leave scripts out of the lock",
		Long: `Leave scripts out of the lock. They stay in git.

- Their #DEP: lines are not pinned, and ` + "`run`" + ` resolves them by name.
- A path is relative to the current directory. A leading / is the project root.
- Quote a wildcard, or the shell expands it first.
- The list is cnt-lock/ignore. A path .gitignore already ignores is not added.`,
		Example: `  condatainer project ignore old_scripts/
  condatainer project ignore 'tmp/*.py'
  condatainer project ignore --list
  condatainer project ignore --remove old_scripts/`,
		SilenceUsage: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if list {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectIgnore(cmd, args, remove, list)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "Remove the paths from the list")
	cmd.Flags().BoolVar(&list, "list", false, "Print the list")
	cmd.MarkFlagsMutuallyExclusive("remove", "list")
	return cmd
}

// ignoreProjectRoot is the project root for the current directory or the one --project names. Without --project it walks up from a subdirectory.
func ignoreProjectRoot() (root, cwd string, err error) {
	if cwd, err = os.Getwd(); err != nil {
		return "", "", err
	}
	if projectDir != "" {
		root, err = lock.RootFor(projectDir, cwd)
	} else {
		root, err = lock.RootAbove(cwd)
	}
	if errors.Is(err, lock.ErrNoProject) {
		return "", "", fmt.Errorf("%w\na project is a directory containing cnt-lock/; name it with --project DIR", err)
	}
	return root, cwd, err
}

func runProjectIgnore(cmd *cobra.Command, args []string, remove, list bool) error {
	root, cwd, err := ignoreProjectRoot()
	if err != nil {
		return err
	}
	stored, err := lock.ReadIgnoreList(root)
	if err != nil {
		return err
	}
	if list {
		for _, line := range stored {
			if !strings.HasPrefix(line, "#") {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
		}
		return nil
	}

	lines := make([]string, len(args))
	for i, arg := range args {
		if lines[i], err = lock.IgnoreLine(root, cwd, arg); err != nil {
			return err
		}
	}
	if remove {
		return removeIgnoreLines(root, stored, lines)
	}
	return addIgnoreLines(root, stored, lines)
}

func addIgnoreLines(root string, stored, lines []string) error {
	updated := slices.Clone(stored)
	for _, line := range lines {
		if slices.Contains(updated, line) {
			utils.PrintMessage("%s is already in the list", line)
			continue
		}
		if !strings.ContainsAny(line, "*?[") {
			covered, err := lock.IgnoredByGit(root, strings.Trim(line, "/"))
			if err != nil {
				return err
			}
			if covered {
				utils.PrintMessage("%s is already ignored by `.gitignore`", line)
				continue
			}
		}
		matched, err := lock.ScriptsMatching(root, line)
		if err != nil {
			return err
		}
		updated = append(updated, line)
		if len(matched) == 0 {
			utils.PrintWarning("%s added, but it matches no script now", line)
			continue
		}
		utils.PrintSuccess("%s added: %d script(s) left out", line, len(matched))
	}
	if len(updated) == len(stored) {
		return nil
	}
	return lock.WriteIgnoreList(root, updated)
}

func removeIgnoreLines(root string, stored, lines []string) error {
	updated := slices.Clone(stored)
	for _, line := range lines {
		i := slices.Index(updated, line)
		if i < 0 {
			return fmt.Errorf("%s is not in the list; `condatainer project ignore --list` shows it", line)
		}
		updated = slices.Delete(updated, i, i+1)
	}
	if err := lock.WriteIgnoreList(root, updated); err != nil {
		return err
	}
	for _, line := range lines {
		utils.PrintSuccess("%s removed", line)
	}
	return nil
}
