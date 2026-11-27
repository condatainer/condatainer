package cmd

import (
	"errors"
	"os"

	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/condatainer/condatainer/internal/utils/units"
	"github.com/spf13/cobra"
)

var (
	unfreezeSize   string
	unfreezeSparse bool
)

const overlayUnfreezeHelp = `Rebuild a standalone writable overlay from a snapshot.

- It holds every file of the snapshot, not only changes on top of it.
- To add to a snapshot instead, run ` + "`overlay create env.img`" + ` beside it.
- It cannot be pinned, locked or published until ` + "`overlay freeze`" + ` packs it.
- A pair name beside the snapshot, such as env.img for env.sqf, is refused.`

var overlayUnfreezeCmd = &cobra.Command{
	Use:   "unfreeze [flags] <snapshot.sqf> <overlay>",
	Short: "Rebuild a writable overlay from a snapshot",
	Long:  overlayUnfreezeHelp,
	Args:  cobra.ExactArgs(2),
	Example: `  condatainer overlay unfreeze env.sqf dev.img         # dev.img here
  condatainer overlay unfreeze env.sqf ./work/dev      # ./work/dev.img
  condatainer overlay unfreeze env.sqf dev.img -s 40G  # dev.img, 40G`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		artifact, given := args[0], args[1]

		if !utils.IsSqf(artifact) {
			ExitWithError("%s is not a snapshot; unfreeze takes the .sqf that freeze produced.", artifact)
		}
		target, err := resolveUnfreezeTarget(artifact, given)
		if err != nil {
			ExitWithError("%v", err)
		}
		if err := makeOverlayParent(target); err != nil {
			ExitWithError("%v", err)
		}

		sizeMB := 0
		if unfreezeSize != "" {
			parsed, err := units.ParseSizeInput(unfreezeSize)
			if err != nil {
				ExitWithError("%v", err)
			}
			sizeMB = int(parsed)
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

// resolveUnfreezeTarget decides where the overlay is written, named as resolveOverlayPath names one.
//   - An existing file is refused: unfreeze starts a new line of development, and that path most likely holds the overlay someone is already developing in.
//   - A path that would pair with the snapshot is refused: the overlay holds everything, so loading the snapshot under it would repeat it.
func resolveUnfreezeTarget(artifact, dest string) (string, error) {
	abs, err := resolveOverlayPath(dest, ".img")
	if err != nil {
		return "", err
	}
	if err := refusePairing(abs, artifact); err != nil {
		return "", err
	}
	return abs, nil
}

// reportUnfreeze says what came back.
func reportUnfreeze(res freeze.UnfreezeResult) {
	utils.PrintMessage("Unfroze %s → %s (%d MB payload, %d entries, in %d MB)",
		res.From, res.Path, res.PayloadMB, res.Entries, res.SizeMB)
}

func init() {
	f := overlayUnfreezeCmd.Flags()
	f.StringVarP(&unfreezeSize, "size", "s", "", "Size; a bare number is GiB (default: payload plus room)")
	f.BoolVarP(&unfreezeSparse, "sparse", "S", false, "Create a sparse overlay image (no pre-allocation)")
}
