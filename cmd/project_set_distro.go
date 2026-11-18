package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/project/orchestrate"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectSelectDistroCmd() *cobra.Command {
	var auto bool
	cmd := &cobra.Command{
		Use:   "set-distro [flags] [distro[@identity]] [identity]",
		Short: "Choose the distro this project uses as its base",
		Long: `Choose the distro whose base image is this project's container root.

- It overrides the configured default_distro for this project.
- An identity picks one installed build. Without it, the first found is used.
- --auto removes the override and goes back to default_distro.
- Changing from another distro asks first. Use --yes to skip the question.
- Pinned overlays, environments and scripts are not changed.`,
		Example: `  condatainer project set-distro ubuntu22
  condatainer project set-distro ubuntu22@b4f7d6e
  condatainer project set-distro ubuntu22 b4f7d6e
  condatainer project set-distro ubuntu22/base@b4f7d6e
  condatainer project set-distro --auto`,
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if auto == (len(args) > 0) {
				return fmt.Errorf("give exactly one of a distro or --auto")
			}
			distro, identity, err := parseDistroArgs(args)
			if err != nil {
				return err
			}
			root, err := projectRoot(true)
			if err != nil {
				return err
			}
			current, err := lock.Load(root)
			if err != nil {
				return err
			}
			target := config.ResolvedDefaultDistro()
			if !auto {
				target = distro
			}
			standing := &project.Standing{Root: root, Lock: current}
			if err := confirmDistroChange(cmd.Context(), cmd.ErrOrStderr(), standing.SelectedDistro(), target); err != nil {
				return err
			}
			var pinned *lock.Pinned
			if auto {
				pinned, err = lock.AutoDistro(root, current, config.ResolvedDefaultDistro(), lock.PinOptions{})
			} else {
				pinned, err = lock.SelectDistro(root, current, distro, identity, lock.PinOptions{})
			}
			if err != nil {
				return err
			}
			upstream := orchestrate.RecordUpstream(cmd.Context(), root, current, pinned)
			if err := lock.Publish(root, current); err != nil {
				return err
			}

			if auto {
				utils.PrintSuccess("Root reset to the configured default: %s", pinned.Name)
			} else {
				utils.PrintSuccess("Root pinned to %s", pinned.Name)
			}
			utils.PrintMessage("  identity %s", pinned.Identity.Digest())
			utils.PrintMessage("  read from %s", pinned.Path)
			for _, artifact := range pinned.Vendored {
				line := "  vendored " + artifact
				if remote, ok := upstream[artifact]; ok {
					line += " (fetchable from " + remote + ")"
				}
				utils.PrintMessage("%s", line)
			}
			for _, other := range pinned.Others {
				utils.PrintMessage("  also installed: %s %s", other.Identity.Digest(), other.Path)
			}
			if len(pinned.Others) > 0 {
				utils.PrintHint("Pick one with `condatainer project set-distro %s <identity>`.", target)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&auto, "auto", false, "Remove the override and use the configured default_distro")
	return cmd
}

// parseDistroArgs reads `distro`, `distro@identity` or `distro identity`, where distro may be written `distro/base` as `store list` shows it.
//   - With no argument it returns empty values.
func parseDistroArgs(args []string) (distro, identity string, err error) {
	if len(args) == 0 {
		return "", "", nil
	}
	distro, identity, joined := strings.Cut(args[0], "@")
	distro = strings.TrimSuffix(distro, "/base")
	switch {
	case distro == "":
		return "", "", fmt.Errorf("give a distro, such as ubuntu22")
	case joined && identity == "":
		return "", "", fmt.Errorf("%q has no identity after @", args[0])
	case len(args) == 2 && joined:
		return "", "", fmt.Errorf("give the identity once: %s@<identity> or %s <identity>", distro, distro)
	case len(args) == 2:
		identity = args[1]
	}
	return distro, identity, nil
}

// errDistroChangeDeclined is the refusal when the user does not confirm a change of distro.
var errDistroChangeDeclined = fmt.Errorf("the distro was not changed")

// confirmDistroChange asks before a project's base moves from one distro to another.
//   - It asks nothing when there is no base yet or the distro stays the same.
//   - --yes answers yes. Input that has ended answers no.
func confirmDistroChange(ctx context.Context, w io.Writer, from, to string) error {
	if from == "" || from == to {
		return nil
	}
	utils.PrintWarning("Changing the base from %s to %s.", from, to)
	fmt.Fprintf(w, "  Overlays and environments built for %s may stop working.\n", from)
	if utils.ShouldAnswerYes() || utils.Confirm(ctx, w, "Change the distro? [y/N]: ") {
		return nil
	}
	return errDistroChangeDeclined
}
