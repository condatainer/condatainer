package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/project"
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
// applyProjectRelocation with dir's value first thing in the command's RunE,
// before anything else reads the working directory.
func RegisterProjectFlags(cmd *cobra.Command, dir *string) {
	cmd.Flags().StringVar(dir, "project", "", "Act as if standing in this directory instead of the current one")
	cmd.Flags().BoolVar(&noProjectRequested, "no-project", false, "Ignore any project found above the current directory")
}

// applyProjectRelocation changes into dir, so every relative path resolved for the rest of the invocation — a script argument, an -o local file, a --bind path, and project lookup itself — resolves against it instead of the directory the command actually started in.
//   - A no-op when dir is empty.
//   - Refused together with --no-project, since the two contradict each other.
func applyProjectRelocation(dir string) error {
	if dir == "" {
		return nil
	}
	if noProjectRequested {
		return fmt.Errorf("--project and --no-project cannot both be set")
	}
	return os.Chdir(dir)
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
