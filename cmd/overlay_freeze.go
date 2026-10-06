package cmd

import (
	"errors"
	"fmt"
	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/settings"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/condatainer/condatainer/internal/image/producer"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	freezeDescription string
	freezeUseTmp      bool
	freezeKeep        bool
)

const overlayFreezeHelp = `Pack a writable overlay into an immutable .sqf snapshot.

- The environment is kept exactly as it is, not rebuilt.
- The snapshot has no recipe behind it, so back it up or push it.
- 'overlay unfreeze' turns it back into a writable .img.

Destination:
- By default the snapshot is <name>.sqf beside the .img, and the .img is removed.
- --keep, or an explicit destination, keeps the .img.

Per-user overlays:
- Freezing env-<user>.img writes env.sqf, the same snapshot a shared env.img uses.
- It replaces the existing env.sqf, and is refused while that is in use or write-protected.
- An .img with a snapshot beside it is frozen together with it. --use-tmp is refused for it.`

var overlayFreezeCmd = &cobra.Command{
	Use:   "freeze [flags] <overlay.img> [artifact.sqf]",
	Short: "Pack a writable overlay into an immutable artifact",
	Long:  overlayFreezeHelp,
	Args:  cobra.RangeArgs(1, 2),
	Example: `  condatainer overlay freeze env.img                     # env.sqf beside it, env.img removed
  condatainer overlay freeze env.img --keep              # env.sqf beside it, env.img kept
  condatainer overlay freeze env-alice.img               # env.sqf, replacing the shared one
  condatainer overlay freeze env.img ./overlays/env.sqf  # That path, env.img kept
  condatainer overlay freeze env.img --zstd-high         # Smaller file, slower to pack`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		source := args[0]

		if !utils.IsImg(source) {
			ExitWithError("%s is not a writable overlay; freeze takes the .img an environment lives in.", source)
		}
		dest := ""
		if len(args) > 1 {
			dest = args[1]
		}
		// The base is only used to translate a directory-level deletion into
		// individual whiteouts against what that directory held; without one,
		// freeze still runs, just without that translation.
		base, err := config.GetBaseImage()
		if err != nil {
			utils.PrintWarning("No base image found (%v); a directory deleted wholesale (not file by file) will not be recorded as deleted, and its original contents will come back when this artifact is used", err)
		}
		// Held across the whole freeze rather than probed: a payload being written
		// to has no defined content to pack, and the pack takes minutes, so a
		// check that released would only prove the overlay was idle when the
		// command started. Shared is the whole requirement — it conflicts with
		// the exclusive lock a writer takes — and it opens read-only, so a
		// pinned overlay stays freezable. It also gates the removal at the end,
		// not only the pack.
		held, err := image.AcquireLock(source, false)
		if err != nil {
			ExitWithError("%v", err)
		}
		defer held.Close() //nolint:errcheck

		// Compression and block size come from the build settings, so a freeze
		// packs the way a build does; the flags override them for this pack only,
		// exactly as create's do.
		compressArgs, blockSize := build.CompressArgs(), build.BlockSize()

		target, err := resolveFreezeTarget(source, dest)
		if err != nil {
			ExitWithError("%v", err)
		}

		// A bare freeze replaces the target it autoloaded against: refuse only if
		// someone is actively reading it (a shared lock probe, the same one an
		// artifact only ever needs for ordinary reading) or it is write-protected.
		// An explicit destination is a flat refusal — that path is a deliberate
		// artifact, and a collision there is more likely a real mistake.
		if utils.FileExists(target) {
			if dest != "" {
				ExitWithError("%s already exists.", target)
			}
			if err := image.CheckAvailable(target, true); err != nil {
				ExitWithError("cannot replace %s: %v", target, err)
			}
		}

		// An .img that continues from a snapshot holds only what changed since, so
		// the artifact is the .img layered over that snapshot: self-contained
		// whether it replaces the snapshot or goes to an explicit destination.
		paired := container.LookupSnapshot(source).Path
		if paired != "" && freezeUseTmp {
			ExitWithError("--use-tmp cannot pack %s over its snapshot %s; freeze without it.", source, paired)
		}

		// Packed to a producer-private path first: a rename over an existing
		// file is atomic, so a reader sees the old snapshot or the new one and
		// never a gap, and a crash mid-pack leaves only an orphaned .part rather
		// than a truncated snapshot at the real path.
		prepared := target + producer.PreparedSuffix
		res, freezeErr := freeze.Freeze(ctx, freeze.Options{
			Image:        source,
			Target:       prepared,
			Description:  freezeDescription,
			Base:         base,
			Snapshot:     paired,
			CompressArgs: compressArgs,
			BlockSize:    blockSize,
			UseTmp:       freezeUseTmp,
			Processors:   build.DefaultSpec().CpusPerTask,
		})
		if freezeErr != nil {
			if errors.Is(freezeErr, freeze.ErrEmptyOverlay) {
				ExitWithError("%s has no payload to freeze.", source)
			}
			ExitWithError("%v", freezeErr)
		}
		if err := os.Rename(prepared, target); err != nil {
			os.Remove(prepared) //nolint:errcheck
			ExitWithError("failed to install %s: %v", target, err)
		}
		res.Path = target

		// The .env sidecar's vars are already folded into the artifact's
		// runtime.json by the pack, so its purpose is served once frozen: it
		// follows the .img's own fate, one rule rather than a separate decision.
		removed := dest == "" && !freezeKeep
		if removed {
			os.Remove(source)          //nolint:errcheck
			os.Remove(source + ".env") //nolint:errcheck
		}
		reportFreeze(res, source, removed)
	},
}

// resolveFreezeTarget decides where the artifact is written.
//   - Only where: every frozen environment is called meta.EnvName.
//   - With no destination, the target is the .img's snapshot slot (container.SnapshotPath), shared by every .img of that name. A bare freeze replaces what is there.
//   - A file in the slot that is not a snapshot is refused.
//   - A destination that is a directory gets the slot's file name.
func resolveFreezeTarget(source, dest string) (string, error) {
	var target string
	slot := container.SnapshotPath(source)
	if dest == "" {
		if lookup := container.LookupSnapshot(source); lookup.Blocked != "" {
			return "", fmt.Errorf("%s exists but is not a snapshot; a bare freeze will not replace it — move it aside or give an explicit destination", lookup.Blocked)
		}
		target = slot
	} else {
		named := filepath.Base(slot)
		switch {
		case strings.HasSuffix(dest, string(filepath.Separator)):
			// A trailing separator says directory whether or not one is there yet.
			// Without this a missing one falls through to the extension branch and
			// becomes a file named ".sqf".
			target = filepath.Join(dest, named)
		default:
			if info, err := os.Stat(dest); err == nil && info.IsDir() {
				// A directory is a place to put it, not the artifact itself.
				target = filepath.Join(dest, named)
			} else if !strings.HasSuffix(dest, ".sqf") {
				target = dest + ".sqf"
			} else {
				target = dest
			}
		}
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	// The pack binds the artifact's directory into the container, so a missing one
	// surfaces there as a mount failure, after the walk and the translation have
	// already run.
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s does not exist; create it or choose another destination", filepath.Dir(abs))
	}
	if dir := imagesDirContaining(abs); dir != "" {
		return "", fmt.Errorf("%s is inside the images directory %s; a frozen environment belongs to its project and is addressed by path, so it is never filed by name", abs, dir)
	}
	return abs, nil
}

// imagesDirContaining reports which images directory holds path, if any. An
// images directory addresses artifacts by filename, and every frozen environment
// is called env, so two would collide there.
func imagesDirContaining(path string) string {
	for _, dir := range config.GetImageSearchPaths() {
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(abs, path); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return abs
		}
	}
	return ""
}

// reportFreeze prints what was produced. What the artifact contains is what
// `info` is for, read back from the file itself.
func reportFreeze(res freeze.Result, source string, removed bool) {
	utils.PrintSuccess("Frozen %s → %s", source, res.Path)
	if removed {
		utils.PrintMessage("  %s removed; the next overlay create starts fresh on top of the snapshot", source)
	}
	if n := res.Translation.Deletions(); n > 0 {
		utils.PrintMessage("  carries %d deletion(s), translated from %s markers",
			n, string(res.Translation.Convention))
	}
}

func init() {
	overlayCmd.AddCommand(overlayFreezeCmd)
	f := overlayFreezeCmd.Flags()
	f.StringVarP(&freezeDescription, "description", "d", "", "Description recorded in the artifact")
	settings.AddFlag(f, "build.block_size", "block-size", settings.Usage("SquashFS block size (default: build.block_size)"))
	f.BoolVar(&freezeUseTmp, "use-tmp", false, "Pack from a temporary copy (faster; needs space for the whole overlay; not for an .img with a snapshot beside it)")
	f.BoolVar(&freezeKeep, "keep", false, "Keep the source .img after a bare freeze (default: remove it)")
	for _, opt := range build.CompressOptions {
		settings.AddSwitch(f, "build.compress_args", opt.Name, opt.Name, settings.Usage(opt.Description))
	}
	registerSettingFlagCompletions(overlayFreezeCmd)
}
