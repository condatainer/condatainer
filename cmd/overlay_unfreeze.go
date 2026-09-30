package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	unfreezeSize   string
	unfreezeSparse bool
)

const overlayUnfreezeHelp = `Rebuild a writable .img from a frozen .sqf.

- The files come back at the paths they had, ready to install into again.
- The result cannot be pinned, locked or published until 'overlay freeze' packs it again.
- The .sqf is left as it is.`

var overlayUnfreezeCmd = &cobra.Command{
	Use:   "unfreeze [flags] <frozen.sqf> [overlay.img]",
	Short: "Rebuild a writable overlay from a frozen environment",
	Long:  overlayUnfreezeHelp,
	Args:  cobra.RangeArgs(1, 2),
	Example: `  condatainer overlay unfreeze env.sqf                 # env.img beside it
  condatainer overlay unfreeze ./overlays/env.sqf      # ./overlays/env.img
  condatainer overlay unfreeze env.sqf dev.img -s 40G  # That path, with room to install more`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		artifact := args[0]
		var given string
		if len(args) > 1 {
			given = args[1]
		}

		if !utils.IsSqf(artifact) {
			ExitWithError("%s is not a frozen environment; unfreeze takes the .sqf that freeze produced.", artifact)
		}
		target, err := resolveUnfreezeTarget(artifact, given)
		if err != nil {
			ExitWithError("%v", err)
		}

		sizeMB := 0
		if unfreezeSize != "" {
			parsed, err := utils.ParseSizeToMB(unfreezeSize)
			if err != nil {
				ExitWithError("%v", err)
			}
			sizeMB = parsed
		}

		res, err := freeze.Unfreeze(ctx, freeze.UnfreezeOptions{
			Artifact: artifact,
			Target:   target,
			SizeMB:   sizeMB,
			UID:      os.Getuid(),
			GID:      os.Getgid(),
			Sparse:   unfreezeSparse,
			StageDir: utils.GetTmpDir(),
		})
		if err != nil {
			if errors.Is(err, freeze.ErrNotFrozen) {
				ExitWithError("%v", err)
			}
			if errors.Is(err, freeze.ErrTooSmall) {
				ExitWithError("%v; pass a larger -s.", err)
			}
			ExitWithError("%v", err)
		}
		reportUnfreeze(res)
	},
}

// resolveUnfreezeTarget decides where the overlay is written, as resolveFreezeTarget
// does: with no destination it lands beside the artifact with the extension changed,
// and a directory is a place to put it. An existing file is refused: unfreeze starts a
// new line of development, and that path most likely holds the overlay someone is
// already developing in.
func resolveUnfreezeTarget(artifact, dest string) (string, error) {
	named := strings.TrimSuffix(filepath.Base(artifact), filepath.Ext(artifact)) + ".img"
	switch {
	case dest == "":
		dest = filepath.Join(filepath.Dir(artifact), named)
	case strings.HasSuffix(dest, string(filepath.Separator)):
		dest = filepath.Join(dest, named)
	default:
		if info, err := os.Stat(dest); err == nil && info.IsDir() {
			dest = filepath.Join(dest, named)
		} else if !utils.IsImg(dest) {
			return "", fmt.Errorf("%s is not a writable overlay path; unfreeze writes an .img", dest)
		}
	}

	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if utils.FileExists(abs) {
		return "", fmt.Errorf("%s already exists", abs)
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s does not exist; create it or choose another destination", filepath.Dir(abs))
	}
	return abs, nil
}

// reportUnfreeze says what came back and, more importantly, what the result is
// not. "I unfroze it, so the project now uses it" is the wrong mental model and
// nothing else will correct it.
func reportUnfreeze(res freeze.UnfreezeResult) {
	utils.PrintMessage("Unfroze %s → %s (%d MB payload, %d entries, in %d MB)",
		res.From, res.Path, res.PayloadMB, res.Entries, res.SizeMB)
	utils.PrintWarning("This is a new development line. A writable overlay has no identity: it cannot be pinned, and it cannot be published.")
	utils.PrintWarning("A project pinning %s still resolves that artifact. Freeze this one again when it is worth keeping.", res.From)
}

func init() {
	overlayCmd.AddCommand(overlayUnfreezeCmd)
	f := overlayUnfreezeCmd.Flags()
	f.StringVarP(&unfreezeSize, "size", "s", "", "Size of the resulting overlay (default: payload plus headroom)")
	f.BoolVarP(&unfreezeSparse, "sparse", "S", false, "Create a sparse overlay image (no pre-allocation)")
}
