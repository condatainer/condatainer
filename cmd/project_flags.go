package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

// noProjectRequested is set by --no-project on exec, e, run, and check. It
// makes every project-aware resolution in the invocation behave as if no
// project were found, regardless of what standing there would otherwise
// resolve.
var noProjectRequested bool

// projectAnnounced tracks whether this invocation has already told the user
// which project it is standing in.
var projectAnnounced bool

// announceProject prints "Project detected: <root>" the first time anything
// in this invocation resolves through a project's lock, and does nothing on
// any later call — overlays, the root, and a script scan can each trigger a
// resolution, and the user needs to see the fact once, not once per thing
// that used it.
func announceProject(root string) {
	announceProjectMarked(root, "")
}

// announceProjectMarked is announceProject with a parenthesised remark, such as a script the lock does not cover.
func announceProjectMarked(root, remark string) {
	if projectAnnounced {
		return
	}
	projectAnnounced = true
	if remark != "" {
		utils.PrintMessage("Project detected: %s (%s)", root, remark)
		return
	}
	utils.PrintMessage("Project detected: %s", root)
}

// RegisterProjectFlags registers --project and --no-project on a command
// that acts in whatever project the caller is standing in. Call
// applyProjectFlag with dir's value first thing in the command's RunE.
func RegisterProjectFlags(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "project", "", "Project root (default: the nearest project above the current directory)")
	cmd.Flags().BoolVar(&noProjectRequested, "no-project", false, "Ignore the project lock")
}

// projectRootFlag is the project root named by --project, or "" when the project is the nearest one above the current directory.
var projectRootFlag string

// applyProjectFlag records the project root named by --project.
//   - The working directory is not changed: a relative path, the default log folder and the job's work directory stay where the command was run.
//   - A no-op when dir is empty.
//   - Refused together with --no-project, since the two contradict each other.
//   - dir must be a project root: it holds cnt-lock/ itself, and no ancestor is searched.
func applyProjectFlag(dir string) error {
	projectRootFlag = ""
	if dir == "" {
		return nil
	}
	if noProjectRequested {
		return fmt.Errorf("--project and --no-project cannot both be set")
	}
	root, err := lock.RootAt(dir)
	if err != nil {
		return fmt.Errorf("--project: %w", err)
	}
	projectRootFlag = root
	return nil
}

// standingProjectHere is the project a run-family command works in: the one --project names, else the nearest one at or above the current directory. It is nil when there is none.
func standingProjectHere() (*project.Standing, error) {
	if projectRootFlag != "" {
		return project.StandingAtRoot(projectRootFlag)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return project.StandingAt(cwd)
}

// printMounts says what each dependency resolved to, as `request → name`.
//   - Outside a project only a request that became a different name is shown.
//   - In a project every dependency is shown, as `request → name@identity`: a dependency no pin answered has no identity and is marked, and an equivalent build shows its own identity and is marked.
//   - A file has no name to expand and is not shown.
func printMounts(mounts []project.Mount, inProject bool) {
	for _, mount := range mounts {
		switch {
		case mount.Name == "":
		case !inProject:
			if mount.Name != mount.Request {
				utils.PrintMessage("%s → %s", mount.Request, mount.Name)
			}
		case mount.Found != "":
			utils.PrintMessage("%s → %s@%s  (equivalent build)", mount.Request, mount.Name, identityPrefix(mount.Found))
		case mount.Live:
			utils.PrintMessage("%s → %s  (not pinned)", mount.Request, mount.Name)
		case mount.Identity != "":
			utils.PrintMessage("%s → %s@%s", mount.Request, mount.Name, identityPrefix(mount.Identity))
		default:
			utils.PrintMessage("%s → %s", mount.Request, mount.Name)
		}
	}
}

// identityPrefix is the part of a digest the store puts after the @ of an address.
func identityPrefix(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) > store.DefaultPrefixChars {
		hex = hex[:store.DefaultPrefixChars]
	}
	return hex
}
