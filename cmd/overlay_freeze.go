package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/image/freeze"
	"github.com/condatainer/condatainer/internal/image/producer"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	freezeDescription string
	freezeBackup      string
)

const overlayFreezeHelp = `Pack a writable overlay into an immutable snapshot.

- The environment is kept exactly as it is, not rebuilt.
- The snapshot has no recipe behind it, so back it up or push it.
- ` + "`overlay unfreeze`" + ` makes a standalone writable copy of it.

Destination:
- By default the snapshot is <name>.sqf beside the .img, which is removed.
- An explicit destination leaves the .img where it is.

Backup:
- --backup moves the .img to the path instead of removing it.
- A paired snapshot that the freeze replaces is kept as sibling .sqf.

Per-user overlays:
- Freezing env-<user>.img writes env.sqf, as a shared env.img does.
- It replaces env.sqf, and is refused while that is in use or write-protected.
- An .img with a snapshot beside it is frozen together with it.`

var overlayFreezeCmd = &cobra.Command{
	Use:   "freeze [flags] <overlay.img> [output.sqf]",
	Short: "Pack a writable overlay into an immutable snapshot",
	Long:  overlayFreezeHelp,
	Args:  cobra.RangeArgs(1, 2),
	Example: `  # env.sqf beside it, env.img removed
  condatainer overlay freeze env.img

  # env.sqf beside it, env.img renamed backup/env-v1.img
  condatainer overlay freeze env.img --backup backup/env-v1

  # env.sqf, replacing the shared one
  condatainer overlay freeze env-alice.img

  # That path, env.img left where it is
  condatainer overlay freeze env.img ./overlays/env.sqf`,
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
			utils.PrintWarning("No base image found (%v); a directory deleted wholesale (not file by file) will not be recorded as deleted, and its original contents will come back when this overlay is used", err)
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

		plan, err := planFreezeBackup(source, dest, freezeBackup, cmd.Flags().Changed("backup"))
		if err != nil {
			ExitWithError("%v", err)
		}
		if err := checkFreezeSource(source, target, dest, plan); err != nil {
			ExitWithError("%v", err)
		}
		if plan.img != "" {
			if err := makeOverlayParent(plan.img); err != nil {
				ExitWithError("%v", err)
			}
		}

		// An .img that continues from a snapshot holds only what changed since, so
		// the artifact is the .img layered over that snapshot: self-contained
		// whether it replaces the snapshot or goes to an explicit destination.
		paired := container.LookupSnapshot(source).Path

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
			Processors:   build.DefaultSpec().CpusPerTask,
		})
		if freezeErr != nil {
			if errors.Is(freezeErr, freeze.ErrEmptyOverlay) {
				ExitWithError("%s has no payload to freeze.", source)
			}
			ExitWithError("%v", freezeErr)
		}
		// The old snapshot gets its backup name once the pack has succeeded. It
		// keeps its own name until the rename below replaces it, so no reader
		// finds the slot empty, and a failure here changes nothing.
		if plan.sqf != "" {
			if err := linkOrCopy(paired, plan.sqf); err != nil {
				os.Remove(prepared) //nolint:errcheck
				ExitWithError("cannot keep %s as %s: %v; nothing was changed", paired, plan.sqf, err)
			}
		}
		if err := os.Rename(prepared, target); err != nil {
			os.Remove(prepared) //nolint:errcheck
			if plan.sqf != "" {
				os.Remove(plan.sqf) //nolint:errcheck
			}
			ExitWithError("failed to install %s: %v", target, err)
		}
		res.Path = target

		// The .env sidecar's vars are already folded into the artifact's
		// runtime.json by the pack, so its purpose is served once frozen: it
		// follows the .img's own fate, one rule rather than a separate decision.
		removed, err := retireFreezeSource(source, dest, plan)
		if err != nil {
			ExitWithError("frozen to %s, but %v", target, err)
		}
		reportFreeze(res, source, removed, plan)
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
		return "", fmt.Errorf("%s is inside the images directory %s; a snapshot belongs to its project and is addressed by path, so it is never filed by name", abs, dir)
	}
	return abs, nil
}

// freezeBackupPlan is what --backup keeps: the .img, and the snapshot the freeze
// replaces when there is one. Both are empty without --backup.
type freezeBackupPlan struct{ img, sqf string }

// planFreezeBackup places the backup files. The path is read as resolveOverlayPath reads one, with the .img's own extension when it has none.
// The snapshot is kept, under the name that pairs with the backup .img, only when the freeze replaces one, which is a bare freeze of an .img with a paired snapshot.
func planFreezeBackup(source, dest, path string, given bool) (freezeBackupPlan, error) {
	if !given {
		return freezeBackupPlan{}, nil
	}
	img, err := resolveOverlayPath(path, filepath.Ext(source))
	if err != nil {
		return freezeBackupPlan{}, fmt.Errorf("--backup: %w", err)
	}
	if err := refusePairing(img, container.SnapshotPath(source)); err != nil {
		return freezeBackupPlan{}, fmt.Errorf("--backup: %w", err)
	}
	plan := freezeBackupPlan{img: img}
	if dest == "" && container.LookupSnapshot(source).Path != "" {
		plan.sqf = container.SnapshotPath(img)
	}
	return plan, nil
}

// linkOrCopy gives src a second name at dst without copying, and copies it when
// the filesystem cannot link. An existing dst is never replaced.
func linkOrCopy(src, dst string) error {
	err := os.Link(src, dst)
	if err == nil || errors.Is(err, os.ErrExist) {
		return err
	}
	return copyFile(src, dst)
}

// moveFile renames src to dst, and copies then removes src when they are on
// different filesystems. src is left in place if the copy fails.
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// copyFile writes src to dst through a .part file renamed into place, so dst is
// never half written. An existing dst is not replaced.
func copyFile(src, dst string) error {
	if utils.FileExists(dst) {
		return fmt.Errorf("%s: %w", dst, os.ErrExist)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	if info, err := in.Stat(); err == nil {
		utils.PrintMessage("Copying %s to %s (%s)", src, dst, utils.FormatSize(info.Size()))
	}
	part := dst + producer.PreparedSuffix
	out, err := utils.CreateFileWritable(part)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(part, dst)
	}
	if err != nil {
		os.Remove(part) //nolint:errcheck
	}
	return err
}

// checkFreezeSource refuses, before the pack, a freeze that would leave the .img
// layered over the snapshot that already holds it, or write over a backup.
func checkFreezeSource(source, target, dest string, plan freezeBackupPlan) error {
	if plan.img == "" {
		if dest != "" && container.PairsWith(source, target) {
			return fmt.Errorf("%s would stay beside %s and layer over it again; pass --backup to rename it, or choose another destination", source, target)
		}
		return nil
	}
	for _, name := range []string{plan.img, plan.img + ".env", plan.sqf} {
		if name != "" && utils.FileExists(name) {
			return fmt.Errorf("%s already exists; move it aside or choose another prefix", name)
		}
	}
	return nil
}

// retireFreezeSource settles the .img and its .env sidecar once the snapshot is
// installed. --backup renames them, a bare freeze removes them, and an explicit
// destination leaves them. It reports whether they were removed.
func retireFreezeSource(source, dest string, plan freezeBackupPlan) (removed bool, err error) {
	switch {
	case plan.img != "":
		if err := moveFile(source, plan.img); err != nil {
			return false, fmt.Errorf("moving %s to %s failed, and it is left in place: %w", source, plan.img, err)
		}
		if err := moveFile(source+".env", plan.img+".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("moving %s.env failed: %w", source, err)
		}
	case dest == "":
		os.Remove(source)          //nolint:errcheck
		os.Remove(source + ".env") //nolint:errcheck
		return true, nil
	}
	return false, nil
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
func reportFreeze(res freeze.Result, source string, removed bool, plan freezeBackupPlan) {
	utils.PrintSuccess("Frozen %s → %s", source, res.Path)
	if removed {
		utils.PrintMessage("  %s removed; the next overlay create starts fresh on top of the snapshot", source)
	}
	switch {
	case plan.sqf != "":
		utils.PrintMessage("  backup: %s and %s, the previous state as a pair", plan.img, plan.sqf)
	case plan.img != "":
		utils.PrintMessage("  backup: %s, the previous state", plan.img)
	}
	if n := res.Translation.Deletions(); n > 0 {
		utils.PrintMessage("  carries %d deletion(s), translated from %s markers",
			n, string(res.Translation.Convention))
	}
}

func init() {
	f := overlayFreezeCmd.Flags()
	f.StringVarP(&freezeDescription, "desc", "d", "", "Description recorded in the overlay")
	settings.AddFlag(f, "build.block_size", "block-size", settings.Usage("SquashFS block size (default: build.block_size)"))
	f.StringVar(&freezeBackup, "backup", "", "Rename the source .img, and keep its snapshot, as `PATH`")
	for _, opt := range build.CompressOptions {
		settings.AddSwitch(f, "build.compress_args", opt.Name, opt.Name, settings.Usage(opt.Description))
	}
	registerSettingFlagCompletions(overlayFreezeCmd)
}
