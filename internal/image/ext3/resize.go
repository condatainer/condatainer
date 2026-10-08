package ext3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/image/tool"
	"github.com/condatainer/condatainer/internal/logging"
)

// Resize adjusts the size of an existing ext3 overlay image to newSizeMB.
//   - It holds the exclusive lock from the first check to the last step, and fails if the image is in use.
//   - Flow: fsck, grow the file if growing, resize2fs to the exact target, shrink the file if shrinking, fsck, allocate.
//   - Sizes are compared against the ext3 filesystem, not the container file's byte size, since the two can diverge.
//   - sparse leaves the image sparse. Otherwise blocks are pre-allocated to the new size, as Create does.
//   - Without fallocate only the part added by growing is filled with zeros. The existing filesystem is never overwritten.
//   - Growing always leaves a hole (os.Truncate), so without allocation a write can hit ENOSPC while the filesystem reports space free.
//   - Allocation runs in both directions and on a no-op resize. fallocate is idempotent, so it costs nothing on an allocated image.
func Resize(ctx context.Context, imagePath string, newSizeMB int, sparse bool) error {
	if err := tool.CheckDependencies([]string{"resize2fs"}); err != nil {
		return err
	}

	absPath, err := filepath.Abs(imagePath)
	if err != nil {
		return fmt.Errorf("failed to resolve path %s: %w", imagePath, err)
	}

	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("overlay image not found: %s", absPath)
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", absPath, err)
	}

	lock, err := lockForWrite(absPath)
	if err != nil {
		return err
	}
	defer lock.Close()

	currentFileBytes := info.Size()
	newSizeBytes := int64(newSizeMB) * 1024 * 1024

	// Current filesystem size from the superblock (block count × block size).
	stats, err := GetStats(absPath)
	if err != nil {
		return err
	}
	fsBytes := stats.TotalBlocks * stats.BlockSize

	log := logging.FromContext(ctx)

	name := filepath.Base(absPath)

	// No-op only when both filesystem and file already match the target. Still
	// allocate: resizing to the current size is how an existing sparse image is
	// filled in without changing its size.
	if fsBytes == newSizeBytes && currentFileBytes == newSizeBytes {
		log.Info(fmt.Sprintf("Size unchanged (%s) for %s",
			fmt.Sprintf("%d MiB", newSizeMB), name))
		if !sparse {
			return AllocateOverlay(ctx, absPath, newSizeMB, newSizeBytes)
		}
		return nil
	}

	// Reject a shrink below current usage up front — cheap, works on a dirty fs
	// (no fsck). resize2fs still enforces the exact minimum later.
	if newSizeBytes < fsBytes {
		if usedBytes, _ := stats.Usage(); newSizeBytes < usedBytes {
			return fmt.Errorf("cannot shrink %s to %s: filesystem already uses %s",
				name,
				fmt.Sprintf("%d MiB", newSizeMB),
				fmt.Sprintf("%d MiB", (usedBytes+1024*1024-1)/(1024*1024)))
		}
	}

	log.Info(fmt.Sprintf("Resizing %s to %s (filesystem currently %s)",
		name,
		fmt.Sprintf("%d MiB", newSizeMB),
		fmt.Sprintf("%d MiB", fsBytes/(1024*1024))))

	// resize2fs refuses to run unless the filesystem was force-checked first.
	if err := checkLocked(ctx, absPath, true); err != nil {
		return err
	}

	// Grow the file first so resize2fs has room to expand into.
	if newSizeBytes > currentFileBytes {
		if err := os.Truncate(absPath, newSizeBytes); err != nil {
			return fmt.Errorf("failed to expand file: %w", err)
		}
	}

	// Resize the filesystem to the exact target (handles grow and shrink).
	action := "expand filesystem"
	if newSizeBytes < fsBytes {
		action = "shrink filesystem"
	}
	log.Info(fmt.Sprintf("%s to %s", action, fmt.Sprintf("%d MiB", newSizeMB)))
	sizeArg := fmt.Sprintf("%dM", newSizeMB)
	if err := tool.RunCommand(ctx, action, absPath, "resize2fs", "-p", absPath, sizeArg); err != nil {
		return err
	}

	// Shrink the file down to reclaim the freed space.
	if newSizeBytes < currentFileBytes {
		if err := os.Truncate(absPath, newSizeBytes); err != nil {
			return fmt.Errorf("failed to truncate file: %w", err)
		}
	}

	if err := checkLocked(ctx, absPath, true); err != nil {
		return err
	}

	// Reserve the blocks only once the resized filesystem checks out.
	if !sparse {
		if err := AllocateOverlay(ctx, absPath, newSizeMB, min(currentFileBytes, newSizeBytes)); err != nil {
			return err
		}
	}

	log.Info(fmt.Sprintf("Overlay image resized to %s: %s",
		fmt.Sprintf("%d MiB", newSizeMB), name), "kind", "success")
	return nil
}
