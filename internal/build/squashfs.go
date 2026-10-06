package build

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/logging"
	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/toolpath"
	"github.com/condatainer/condatainer/internal/utils"
)

// createSquashfs packs sourceDir (and metaDir, if any) into targetPath with mksquashfs, run directly on the host — no container, no Apptainer.
//   - Every source is a plain host path (a sandbox or the payload directory). isData picks the data block size over the general one.
//   - The caller must Cleanup(true) on an error.
func createSquashfs(ctx context.Context, b *BuildObject, isData bool, sourceDir, metaDir, targetPath string) error {
	if absTarget, err := filepath.Abs(targetPath); err == nil {
		targetPath = absTarget
	}

	done := watchContext(ctx, "SquashFS creation")
	defer close(done)

	mksquashfsBin, err := toolpath.Resolve("mksquashfs")
	if err != nil {
		return err
	}
	b.captureMksquashfsVersion(ctx, mksquashfsBin)

	ncpus := b.effectiveNcpus()
	compressArgs := CompressArgs()
	blockSize := BlockSize()
	if isData {
		blockSize = DataBlockSize()
	}

	log := logging.FromContext(ctx)
	io := execpkg.IOFromContext(ctx)

	if err := b.stagePayloadKey(ctx, sourceDir, metaDir); err != nil {
		return err
	}

	source := sourceDir
	if b.ws.UsesSandbox() {
		source = b.ws.Sandbox
	}
	sources, keepAsDirectory := packSources(b, sourceDir, metaDir)
	log.Debug("creating SquashFS", "name", b.spec.Image.Name, "sources", sources)
	log.Info("Packing SquashFS", "source", source, "target", targetPath)
	script := squashfsScript(mksquashfsBin, sources, targetPath, ncpus, blockSize, compressArgs, keepAsDirectory)
	runErr := runHostScript(ctx, script, io)

	if runErr != nil {
		if isCancelledByUser(runErr) {
			return ErrBuildCancelled
		}
		return fmt.Errorf("failed to create SquashFS: %w", runErr)
	}

	return nil
}

// packSources decides mksquashfs's source list, and whether a lone source
// keeps its own directory name in the archive, for the workspace's mode.
func packSources(b *BuildObject, sourceDir, metaDir string) (sources []string, keepAsDirectory bool) {
	if b.ws.UsesSandbox() {
		// The sandbox is the container root apptainer wrote: its own contents
		// belong at the archive root, dotfiles and all, not nested under the
		// directory's name.
		return []string{b.ws.Sandbox}, false
	}
	sources = []string{sourceDir}
	if metaDir != "" {
		// A host path already named .cnt (staged by stageMetadata), so it is
		// its own source — mksquashfs names an archive root after a source's
		// basename and cannot rename one.
		sources = append(sources, metaDir)
	}
	return sources, true
}

// runHostScript runs script with /bin/bash directly on the host, wiring the
// caller's own IO — no container involved.
func runHostScript(ctx context.Context, script string, io execpkg.IO) error {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", script)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = io.Stdin, io.Stdout, io.Stderr
	return cmd.Run()
}

// squashfsScript renders the mksquashfs invocation. mksquashfsBin is already resolved
// (toolpath.Resolve, in createSquashfs), so packing never triggers a libexec download.
//
//   - keepAsDirectory only affects a lone source: it keeps that directory instead of
//     unwrapping it into the archive root. A sandbox turns it off, so its contents sit
//     at the root, dotfiles included.
//   - -no-xattrs: nothing reads them back, and shared filesystems hand mksquashfs
//     attributes it cannot store and unsquashfs ones it cannot restore unprivileged.
//   - -quiet hides the final statistics and keeps the progress bar, the useful output.
//   - Quiet mode adds -no-progress and drops the announcement too.
func squashfsScript(mksquashfsBin string, sources []string, targetPath string, ncpus int, blockSize, compressArgs string, keepAsDirectory bool) string {
	keep := ""
	if keepAsDirectory {
		keep = "-keep-as-directory "
	}
	announce, progress := `echo "Packing overlay to SquashFS..."`, ""
	if utils.QuietMode {
		announce, progress = "", "-no-progress "
	}
	return fmt.Sprintf(`
trap 'exit 130' INT TERM
%s
%s %s %s -processors %d -b %s %s-all-root -no-xattrs -quiet %s%s
`, announce, mksquashfsBin, strings.Join(sources, " "), targetPath, ncpus, blockSize, keep, progress, compressArgs)
}
