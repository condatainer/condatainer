// Package ext3 creates, resizes, checks and inspects writable ext3 overlay images.
package ext3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/condatainer/condatainer/internal/image/tool"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/toolpath"
	"github.com/condatainer/condatainer/internal/utils"
)

// ---------------------------------------------------------
// 1. Filesystem Profiles and Options
// ---------------------------------------------------------

// Profile defines the tuning parameters for the filesystem.
type Profile struct {
	InodeRatio   int // -i: bytes per inode
	ReservedPerc int // -m: percentage (0-100)
}

// DefaultSize is the size of a new overlay when none is given: the create flag, the helper prompt and the dashboard form.
const DefaultSize = "10G"

// CreateOptions holds all configuration options for creating an overlay image.
type CreateOptions struct {
	Path           string  // Path to the overlay image file
	SizeMB         int     // Size in megabytes
	UID            int     // Owner user ID
	GID            int     // Owner group ID
	Profile        Profile // Filesystem tuning profile
	Sparse         bool    // Create sparse file (vs allocated)
	FilesystemType string  // Filesystem type: "ext3" or "ext4" (default: "ext3")
	Quiet          bool    // Suppress detailed specs output
}

var (
	// ProfileSmall is tuned for environments with many small files (Conda packages, Python/site-packages).
	ProfileSmall = Profile{InodeRatio: 4096, ReservedPerc: 3}

	// ProfileDefault is a general-purpose profile (Linux default 16K).
	ProfileDefault = Profile{InodeRatio: 16384, ReservedPerc: 3}

	// ProfileLarge is optimized for large files (genomes, FASTA, databases).
	ProfileLarge = Profile{InodeRatio: 1048576, ReservedPerc: 3}
)

// ParseProfile resolves a profile name (small/balanced/large, or an alias) to its
// Profile. An empty name selects the default; an unrecognized name is an error.
func ParseProfile(name string) (Profile, error) {
	switch strings.ToLower(name) {
	case "small", "conda", "python":
		return ProfileSmall, nil
	case "large", "data", "genome":
		return ProfileLarge, nil
	case "balanced", "default", "":
		return ProfileDefault, nil
	default:
		return Profile{}, fmt.Errorf("unknown profile %q (want: small/conda/python, balanced/default, large/data/genome)", name)
	}
}

// ---------------------------------------------------------
// 2. Internal helpers
// ---------------------------------------------------------

// validateOpts normalizes and validates CreateOptions in-place.
func validateOpts(opts *CreateOptions) error {
	if opts.FilesystemType == "" {
		opts.FilesystemType = "ext3"
	}
	fsType := strings.ToLower(opts.FilesystemType)
	if fsType != "ext3" && fsType != "ext4" {
		return fmt.Errorf("unsupported filesystem type '%s': must be ext3 or ext4", opts.FilesystemType)
	}
	opts.FilesystemType = fsType
	return nil
}

// LogCreating logs the one-line description of the overlay about to be created, unless opts.Quiet.
func LogCreating(ctx context.Context, opts *CreateOptions) {
	if opts.Quiet {
		return
	}
	kind := "allocated"
	if opts.Sparse {
		kind = "sparse"
	}
	if opts.UID == 0 && opts.GID == 0 {
		kind += " fakeroot"
	}
	logging.FromContext(ctx).Info(fmt.Sprintf("Creating %s overlay %s | size %d MiB | %s | inode ratio %d | reserved %d%%",
		kind, filepath.Base(opts.Path), opts.SizeMB, opts.FilesystemType, opts.Profile.InodeRatio, opts.Profile.ReservedPerc))
}

// createOverlayFile runs dd + mke2fs + debugfs to build a raw overlay at filePath.
// The file is created sparse, then pre-allocated unless opts.Sparse.
func createOverlayFile(ctx context.Context, opts *CreateOptions, filePath string) error {
	if err := tool.CheckDependencies([]string{"dd", "mke2fs", "debugfs"}); err != nil {
		return err
	}
	cleanup := func() { os.Remove(filePath) }

	// 1. Create raw file
	if err := createRawFile(ctx, opts, filePath); err != nil {
		cleanup()
		return err
	}

	// 2. Format filesystem (mke2fs)
	if err := tool.RunCommand(ctx, "format", opts.Path, "mke2fs",
		"-t", opts.FilesystemType,
		"-E", "nodiscard",
		"-i", fmt.Sprintf("%d", opts.Profile.InodeRatio),
		"-m", fmt.Sprintf("%d", opts.Profile.ReservedPerc),
		"-F", filePath); err != nil {
		cleanup()
		return err
	}

	// 3. Inject overlay structure (debugfs)
	var script strings.Builder
	script.WriteString("mkdir upper\n")
	script.WriteString("mkdir work\n")
	fmt.Fprintf(&script, "set_inode_field upper uid %d\n", opts.UID)
	fmt.Fprintf(&script, "set_inode_field upper gid %d\n", opts.GID)
	fmt.Fprintf(&script, "set_inode_field work uid %d\n", opts.UID)
	fmt.Fprintf(&script, "set_inode_field work gid %d\n", opts.GID)
	script.WriteString("quit\n")

	debugfsPath, err := toolpath.Resolve("debugfs")
	if err != nil {
		cleanup()
		return err
	}
	cmd := exec.CommandContext(ctx, debugfsPath, "-w", filePath)
	cmd.Stdin = strings.NewReader(script.String())
	var debugBuf bytes.Buffer
	if w := logging.WriterFromCtx(ctx); w != nil {
		cmd.Stdout = w
		cmd.Stderr = w
	} else {
		cmd.Stdout = &debugBuf
		cmd.Stderr = &debugBuf
	}
	if execErr := cmd.Run(); execErr != nil {
		cleanup()
		return &tool.Error{
			Op:      "inject structure",
			Path:    opts.Path,
			Tool:    "debugfs",
			Output:  debugBuf.String(),
			BaseErr: execErr,
		}
	}

	// 4. Set permissions
	utils.ShareWithParentGroup(filePath)

	return nil
}

// ---------------------------------------------------------
// 3. Public API
// ---------------------------------------------------------

// createRawFile makes the empty image file at filePath, sparse when opts.Sparse and pre-allocated otherwise.
func createRawFile(ctx context.Context, opts *CreateOptions, filePath string) error {
	err := tool.RunCommand(ctx, "create_file", opts.Path, "dd",
		"if=/dev/zero", "of="+filePath, "bs=1M", "count=0", fmt.Sprintf("seek=%d", opts.SizeMB))
	if err != nil {
		return err
	}
	if !opts.Sparse {
		return AllocateOverlay(ctx, filePath, opts.SizeMB, 0)
	}
	return nil
}

// fallocate is swapped by tests to simulate a filesystem without it.
var fallocate = syscall.Fallocate

// zeroChunk is the size of one write when zeros stand in for fallocate.
const zeroChunk = 1 << 20

// AllocateOverlay reserves the blocks of the first sizeMB of the image file at path.
//   - fallocate reserves them without writing. Any failure but "not supported" is returned.
//   - Where the filesystem has no fallocate, zeros are written from zeroFrom to the end, then synced so a refused write is reported.
//   - Bytes before zeroFrom hold data that must stay, so they stay unreserved there and a warning says so.
func AllocateOverlay(ctx context.Context, path string, sizeMB int, zeroFrom int64) error {
	log := logging.FromContext(ctx)
	name := strings.TrimSuffix(filepath.Base(path), ".partial")
	size := int64(sizeMB) << 20
	log.Info(fmt.Sprintf("Allocating %d MiB at %s", sizeMB, name))

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("allocate %s: %w", name, err)
	}
	defer f.Close()

	err = fallocate(int(f.Fd()), 0, 0, size)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EOPNOTSUPP) && !errors.Is(err, syscall.ENOSYS) {
		return fmt.Errorf("allocate %s: %w", name, err)
	}

	if zeroFrom > 0 {
		log.Warn(fmt.Sprintf("This filesystem cannot reserve space; the first %d MiB of %s stay sparse", zeroFrom>>20, name))
	}
	if zeroFrom >= size {
		return nil
	}
	log.Info(fmt.Sprintf("This filesystem cannot reserve space; writing zeros to %s", name))
	buf := make([]byte, zeroChunk)
	for off := zeroFrom; off < size; off += zeroChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := int64(len(buf))
		if size-off < n {
			n = size - off
		}
		if _, err := f.WriteAt(buf[:n], off); err != nil {
			return fmt.Errorf("allocate %s: %w", name, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("allocate %s: %w", name, err)
	}
	return nil
}

// CreateDirectly builds a blank overlay at opts.Path.
func CreateDirectly(ctx context.Context, opts *CreateOptions) error {
	if err := validateOpts(opts); err != nil {
		return err
	}

	destDir := filepath.Dir(opts.Path)
	if err := utils.MkdirAllShared(destDir); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	LogCreating(ctx, opts)

	return createOverlayFile(ctx, opts, opts.Path)
}
