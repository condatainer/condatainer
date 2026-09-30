package ext3

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/image/tool"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/toolpath"
	"github.com/condatainer/condatainer/internal/utils"
)

var (
	// ErrTooSmall means the staged payload cannot fit in the requested image size.
	ErrTooSmall = errors.New("overlay size too small for its contents")
	// ErrCreateInProgress means another process is writing the same overlay.
	ErrCreateInProgress = errors.New("overlay is being created by another process")
	// ErrTooManyFiles means the staged payload has more entries than the profile's inode ratio allows.
	ErrTooManyFiles = errors.New("too many files for the overlay's inode ratio")
)

const (
	packBlockSize = 4096
	inodeSize     = 256
	opaqueMarker  = ".wh..wh..opq"
)

// Pack builds the overlay at opts.Path from stage, a directory holding upper/ and work/.
//   - The image is written to <path>.partial, locked while it is written, and renamed on success, so a failed run leaves nothing at the destination.
//   - Opaque markers left by fuse-overlayfs are dropped and work/ is emptied.
//   - A root owner is recorded by running mke2fs in a root-mapped user namespace; any other owner is applied to the finished image.
func Pack(ctx context.Context, stage string, opts *CreateOptions) error {
	if err := validateOpts(opts); err != nil {
		return err
	}
	if err := tool.CheckDependencies([]string{"dd", "mke2fs", "debugfs"}); err != nil {
		return err
	}
	if err := utils.MkdirAllShared(filepath.Dir(opts.Path)); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	upper := filepath.Join(stage, "upper")
	entries, payloadMB, err := prepareStage(stage, upper)
	if err != nil {
		return err
	}
	if err := checkFits(opts, entries, payloadMB); err != nil {
		return err
	}

	log := logging.FromContext(ctx)
	if !opts.Quiet {
		log.Info(fmt.Sprintf("Packing %s | payload %d MiB | %d files",
			filepath.Base(opts.Path), payloadMB, entries))
	}

	partial := opts.Path + ".partial"
	lock, err := lockPartial(ctx, partial)
	if err != nil {
		return err
	}
	defer lock.Close()
	done := false
	defer func() {
		if !done {
			os.Remove(partial)
		}
	}()

	if err := createRawFile(ctx, opts, partial); err != nil {
		return err
	}
	mkfs := []string{
		"-q", "-t", opts.FilesystemType,
		"-i", fmt.Sprintf("%d", opts.Profile.InodeRatio),
		"-m", fmt.Sprintf("%d", opts.Profile.ReservedPerc),
		"-d", stage, "-F", partial,
	}
	chown := opts.UID != os.Getuid() || opts.GID != os.Getgid()
	if chown && opts.UID == 0 && opts.GID == 0 && rootNamespaceAvailable(ctx) {
		// Inside the namespace the caller is root, so mke2fs records 0:0.
		mke2fs, err := toolpath.Resolve("mke2fs")
		if err != nil {
			return err
		}
		err = tool.RunCommandBound(ctx, "pack", opts.Path, "unshare", append([]string{"-r", mke2fs}, mkfs...)...)
		if err != nil {
			return err
		}
		chown = false
	} else if err := tool.RunCommandBound(ctx, "pack", opts.Path, "mke2fs", mkfs...); err != nil {
		return err
	}
	if chown {
		if err := ChownRecursively(ctx, partial, opts.UID, opts.GID, "/"); err != nil {
			return err
		}
	}

	utils.ShareWithParentGroup(partial)
	if err := os.Rename(partial, opts.Path); err != nil {
		return fmt.Errorf("install overlay: %w", err)
	}
	done = true
	return nil
}

// lockPartial creates path, or takes over one left by a run that died, and holds a write lock on it.
//   - A held lock means another create is running, and is ErrCreateInProgress.
//   - A leftover file is emptied with a warning.
//   - The lock is on the file that path names now: a file renamed away while waiting is not taken.
func lockPartial(ctx context.Context, path string) (*utils.FileLock, error) {
	for range 3 {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, utils.PermFile)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", path, err)
		}
		lock, err := utils.LockOpenFile(f, true)
		if err != nil {
			f.Close()
			if errors.Is(err, utils.ErrLockConflict) {
				return nil, fmt.Errorf("%w: %s", ErrCreateInProgress, path)
			}
			return nil, err
		}
		held, _ := f.Stat()
		if now, err := os.Stat(path); err != nil || held == nil || !os.SameFile(held, now) {
			lock.Close()
			continue
		}
		if held.Size() > 0 {
			logging.FromContext(ctx).Warn(fmt.Sprintf("Removing incomplete overlay from an earlier run: %s", path))
			if err := f.Truncate(0); err != nil {
				lock.Close()
				return nil, fmt.Errorf("reset %s: %w", path, err)
			}
		}
		return lock, nil
	}
	return nil, fmt.Errorf("%w: %s keeps changing", ErrCreateInProgress, path)
}

// rootNamespaceAvailable reports whether an unprivileged user namespace mapping the caller to root can be created.
func rootNamespaceAvailable(ctx context.Context) bool {
	return tool.RunCommand(ctx, "probe", "", "unshare", "-r", "true") == nil
}

// prepareStage normalizes stage for mke2fs and measures upper.
// It returns the number of entries and the payload size in MiB, rounded up to whole blocks.
func prepareStage(stage, upper string) (entries, payloadMB int, err error) {
	work := filepath.Join(stage, "work")
	if err := os.RemoveAll(work); err != nil {
		return 0, 0, fmt.Errorf("reset work dir: %w", err)
	}
	if err := os.Mkdir(work, 0o755); err != nil {
		return 0, 0, fmt.Errorf("reset work dir: %w", err)
	}

	var bytes int64
	err = filepath.WalkDir(upper, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == opaqueMarker {
			return os.Remove(path)
		}
		entries++
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() || info.IsDir() {
			bytes += (max(info.Size(), 1) + packBlockSize - 1) / packBlockSize * packBlockSize
		}
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("scan %s: %w", upper, err)
	}
	return entries, int(bytes/(1024*1024)) + 1, nil
}

// checkFits refuses a payload that cannot fit in the image, before any image is written.
func checkFits(opts *CreateOptions, entries, payloadMB int) error {
	inodes := opts.SizeMB * 1024 * 1024 / opts.Profile.InodeRatio
	if entries > inodes {
		return fmt.Errorf("%w: %d files, %d inodes at 1 per %d bytes; use a smaller inode ratio (-p small)",
			ErrTooManyFiles, entries, inodes, opts.Profile.InodeRatio)
	}
	tableMB := inodes * inodeSize / (1024 * 1024)
	if payloadMB+tableMB > opts.SizeMB {
		return fmt.Errorf("%w: needs about %d MiB, requested %d MiB; use a larger -s",
			ErrTooSmall, payloadMB+tableMB, opts.SizeMB)
	}
	return nil
}
