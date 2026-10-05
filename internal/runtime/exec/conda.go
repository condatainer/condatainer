package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/image/ext3"
	"github.com/condatainer/condatainer/internal/image/tool"
	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
)

// DescribeInitialCondaPackages returns user-facing text for the initial package list.
func DescribeInitialCondaPackages(pkgs []string) string {
	if len(pkgs) == 0 {
		return "empty conda environment"
	}
	return strings.Join(pkgs, " ")
}

// CreateCondaOverlay creates a new ext3 overlay at opts.Path.
//   - With nothing to install it writes a blank image there, and Conda is set up on the first install.
//   - Otherwise it checks the host tools and micromamba, installs into a directory overlay on local tmp, then packs that into the image at opts.Path.
//   - The caller provides the container root the install runs in.
//   - postInstallCmd runs in the staged overlay after the install (empty = skip).
//   - fakeroot makes the image root-owned; without it a root uid/gid in opts means the current user.
func CreateCondaOverlay(ctx context.Context, opts *ext3.CreateOptions, pkgs []string, postInstallCmd string, fakeroot bool, io IO) error {
	if !fakeroot && opts.UID == 0 && opts.GID == 0 {
		opts.UID = os.Getuid()
		opts.GID = os.Getgid()
	}
	if len(pkgs) == 0 && postInstallCmd == "" {
		return ext3.CreateDirectly(ctx, opts)
	}

	// Checked before the install, which is the slow step, so a missing tool fails at once.
	if err := tool.CheckDependencies([]string{"dd", "mke2fs", "debugfs"}); err != nil {
		return err
	}
	if err := libexec.EnsureMicromamba(ctx); err != nil {
		return err
	}

	tmpDir := utils.GetTmpDir()
	if err := utils.EnsureTmpSubdir(tmpDir); err != nil {
		return fmt.Errorf("failed to create tmp dir %s: %w", tmpDir, err)
	}
	defer utils.RemoveDirIfEmpty(tmpDir)
	stage, err := os.MkdirTemp(tmpDir, "overlay-create-")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stage)

	// Setup treats a directory holding upper/ and work/ as the writable layer.
	overlayDir := stage
	for _, sub := range []string{"upper", "work"} {
		if err := os.MkdirAll(filepath.Join(overlayDir, sub), 0o755); err != nil {
			return fmt.Errorf("create staging dir: %w", err)
		}
	}
	ext3.LogCreating(ctx, opts)
	logging.FromContext(ctx).Info(fmt.Sprintf("Installing in local tmp dir %s, then packing into %s", stage, filepath.Base(opts.Path)))

	// The stage is not beside the destination, so the paired snapshot is looked up
	// against the destination and mounted explicitly.
	snapshot := container.LookupSnapshot(opts.Path).Path

	if err := InitCondaEnv(ctx, overlayDir, snapshot, pkgs, fakeroot, io); err != nil {
		return err
	}
	if postInstallCmd != "" {
		if err := RunPostInstall(ctx, overlayDir, snapshot, postInstallCmd, fakeroot, io); err != nil {
			return fmt.Errorf("post-install failed: %w", err)
		}
	}
	return ext3.Pack(ctx, overlayDir, opts)
}

// InitCondaEnv creates a new conda environment inside imgPath with `condatainer env install`, then cleans the micromamba package cache to shrink the overlay.
//   - Use it for the first environment on a fresh image. InstallPackages adds to an existing one.
//   - snapshot, when non-empty, is mounted read-only beneath imgPath. Pass container.LookupSnapshot's result when imgPath is a staging directory that will not autoload one (see CreateCondaOverlay). Otherwise "".
func InitCondaEnv(ctx context.Context, imgPath, snapshot string, pkgs []string, fakeroot bool, io IO) error {
	if len(pkgs) == 0 {
		return nil
	}
	overlays := condaScratchOverlays(imgPath, snapshot)
	cmd := append([]string{container.BoundExecPath, "env", "install", "-y"}, pkgs...)
	if err := Run(ctx, Options{
		Overlays:    overlays,
		WritableImg: true,
		Fakeroot:    fakeroot,
		Command:     cmd,
		HidePrompt:  true,
	}, io); err != nil {
		return err
	}
	// Non-fatal cache clean to reduce overlay size.
	_ = Run(ctx, Options{
		Overlays:    overlays,
		WritableImg: true,
		Fakeroot:    fakeroot,
		Command:     []string{container.BoundExecPath, "env", "clean", "-a", "-y", "-q"},
		HidePrompt:  true,
	}, IO{})
	return nil
}

// condaScratchOverlays is the overlay list for a conda operation on imgPath,
// with snapshot appended when given.
func condaScratchOverlays(imgPath, snapshot string) []string {
	if snapshot == "" {
		return []string{imgPath}
	}
	return []string{imgPath, snapshot}
}

// InstallPackages installs additional conda packages into an existing base
// environment inside imgPath using `condatainer env install` (respects the overlay's .condarc channels).
func InstallPackages(ctx context.Context, imgPath string, pkgs []string, fakeroot bool, io IO) error {
	return Run(ctx, Options{
		Overlays:    []string{imgPath},
		WritableImg: true,
		Fakeroot:    fakeroot,
		Command:     append([]string{container.BoundExecPath, "env", "install", "-y"}, pkgs...),
		HidePrompt:  true,
	}, io)
}

// RemovePackages removes conda packages from the base environment inside imgPath.
func RemovePackages(ctx context.Context, imgPath string, pkgs []string, fakeroot bool, io IO) error {
	return Run(ctx, Options{
		Overlays:    []string{imgPath},
		WritableImg: true,
		Fakeroot:    fakeroot,
		Command:     append([]string{container.BoundExecPath, "env", "remove", "-y"}, pkgs...),
		HidePrompt:  true,
	}, io)
}

// RunPostInstall runs an arbitrary command inside imgPath after package
// installation. snapshot is as in InitCondaEnv.
func RunPostInstall(ctx context.Context, imgPath, snapshot, postCmd string, fakeroot bool, io IO) error {
	return Run(ctx, Options{
		Overlays:    condaScratchOverlays(imgPath, snapshot),
		WritableImg: true,
		Fakeroot:    fakeroot,
		Command:     []string{"bash", "-c", postCmd},
		HidePrompt:  true,
	}, io)
}
