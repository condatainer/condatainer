package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/conda"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:               "export [flags] <overlay.img>",
	Short:             "Export the Conda environment in a writable .img overlay",
	Long:              overlayExportHelp,
	Example:           overlayExportExample,
	Args:              cobra.ExactArgs(1),
	SilenceUsage:      true,
	ValidArgsFunction: completeImages,
	RunE:              runExportOverlay,
}

func init() {
	registerExportFlags(exportCmd)
}

// resolveOverlayArg resolves an overlay argument to an absolute path: an installed
// overlay by name (as `exec -o` resolves it), else a file or directory path.
func resolveOverlayArg(arg string) (string, error) {
	return installedOverlayFile(context.Background(), arg)
}

// openExportOutput returns a writer, a close func, and a destination label.
// Empty prefix writes to stdout; otherwise to <prefix><ext>.
func openExportOutput(prefix, ext string) (io.Writer, func() error, string, error) {
	if prefix == "" {
		return os.Stdout, func() error { return nil }, "stdout", nil
	}
	path := prefix + ext
	f, err := utils.CreateFileWritable(path)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create %s: %w", path, err)
	}
	return f, f.Close, path, nil
}

// reorderChannelBlock rewrites a conda env YAML's `channels:` block into the given priority order, fixing micromamba's alphabetical sort which can break re-solve.
//   - Channels not in priority are prepended, matching conda's from_environment.
//   - Returns the input unchanged if there is no channels block to reorder.
func reorderChannelBlock(yaml []byte, priority []string) []byte {
	lines := strings.Split(string(yaml), "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "channels:" {
			start = i
			break
		}
	}
	if start < 0 {
		return yaml
	}
	end := start + 1
	lineOf := map[string]string{}
	var names []string
	for end < len(lines) {
		t := strings.TrimSpace(lines[end])
		if !strings.HasPrefix(t, "- ") {
			break
		}
		name := strings.Trim(strings.TrimSpace(t[2:]), `"'`)
		lineOf[name] = lines[end]
		names = append(names, name)
		end++
	}
	if len(names) < 2 {
		return yaml
	}

	seen := map[string]bool{}
	known := make([]string, 0, len(names))
	for _, p := range priority {
		if l, ok := lineOf[p]; ok && !seen[p] {
			known = append(known, l)
			seen[p] = true
		}
	}
	// Channels not in .condarc are prepended, matching conda's
	// channels.insert(0, …), so a directly `-c`-installed channel keeps priority.
	ordered := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			ordered = append(ordered, lineOf[n])
			seen[n] = true
		}
	}
	ordered = append(ordered, known...)

	out := append([]string{}, lines[:start+1]...)
	out = append(out, ordered...)
	out = append(out, lines[end:]...)
	return []byte(strings.Join(out, "\n"))
}

// runExportOverlay exports the live Conda environment in a writable .img.
//   - Only an .img: it is mutable working state, so exporting its current packages is the only way to capture what it has become.
//   - An installed image is immutable and carries its own metadata, and is reproduced by rebuilding from its recipe rather than by recovering one from the image.
func runExportOverlay(cmd *cobra.Command, args []string) error {
	overlayPath, err := resolveOverlayArg(args[0])
	if err != nil {
		return err
	}
	prefix, _ := cmd.Flags().GetString("prefix")

	// Announce the destination unless it is already visible: writing to a live
	// terminal (no --prefix, stdout not redirected) shows the export directly.
	announce := prefix != "" || !utils.IsInteractiveShell()

	if !utils.IsImg(overlayPath) {
		cmd.SilenceUsage = true
		return fmt.Errorf("export needs a writable .img overlay; %s is not one", overlayPath)
	}

	// Reading an .img read-only does not trip Apptainer's ext3 lock, so probe it
	// to fail on a concurrent writable session instead of exporting stale data.
	if err := image.CheckAvailable(overlayPath, false); err != nil {
		cmd.SilenceUsage = true
		return err
	}

	ResolveFlagAlias(cmd, "no-build", "no-builds")

	// Build micromamba export args
	flagsArgs := []string{}
	explicit, _ := cmd.Flags().GetBool("explicit")
	noMD5, _ := cmd.Flags().GetBool("no-md5")
	noBuild, _ := cmd.Flags().GetBool("no-build")
	channelSubdir, _ := cmd.Flags().GetBool("channel-subdir")
	fromHistory, _ := cmd.Flags().GetBool("from-history")

	if explicit {
		flagsArgs = append(flagsArgs, "-e")
	}
	if noMD5 {
		flagsArgs = append(flagsArgs, "--no-md5")
	}
	if noBuild {
		flagsArgs = append(flagsArgs, "--no-builds")
	}
	if channelSubdir {
		flagsArgs = append(flagsArgs, "--channel-subdir")
	}
	if fromHistory {
		flagsArgs = append(flagsArgs, "--from-history")
	}

	// A writable .img keeps its environment at the one fixed location.
	envPrefix := container.EnvPrefix
	if !image.PathExists(overlayPath, envPrefix+"/conda-meta") {
		cmd.SilenceUsage = true
		return fmt.Errorf("overlay %s does not contain a conda environment at %s (missing conda-meta)", overlayPath, envPrefix)
	}

	// stderr only, so the export on stdout is untouched.
	if names, err := image.TopLevel(overlayPath); err == nil {
		if outside := entriesOutsideEnv(names); len(outside) > 0 {
			utils.PrintWarning("%s also holds files outside %s (%s); the export covers only the Conda environment. `condatainer overlay freeze` keeps everything.",
				filepath.Base(overlayPath), envPrefix, strings.Join(outside, ", "))
		}
	}

	// Extension follows the export format: an explicit spec (.txt) or YAML.
	ext := ".yml"
	if explicit {
		ext = ".txt"
	}
	out, closeOut, dest, err := openExportOutput(prefix, ext)
	if err != nil {
		return err
	}
	defer closeOut() //nolint:errcheck
	if announce {
		utils.PrintMessage("Exporting conda environment from %s to %s", envPrefix, dest)
	}

	// Build final command: micromamba -p <prefix> env export [flags]
	cmdArgs := []string{"micromamba", "-p", envPrefix, "env", "export"}
	if len(flagsArgs) > 0 {
		cmdArgs = append(cmdArgs, flagsArgs...)
	}

	opts := exec.Options{
		Overlays:    []string{overlayPath},
		Command:     cmdArgs,
		WritableImg: false,
		EnvSettings: []string{},
		HidePrompt:  true,
	}

	// Drop Apptainer's non-error log lines from stderr so they don't pollute
	// the export; ERROR:/FATAL: and any real errors still pass through.
	stderr := apptainer.NewApptainerFilter(os.Stderr, apptainer.ApptainerMessageNonErrorPrefixes...)
	defer stderr.Flush() //nolint:errcheck

	// micromamba sorts the channels: block alphabetically, which can break
	// re-solve. When the overlay pins channel priority in its .condarc, capture
	// the YAML and reorder to match. The explicit (.txt) format has no channels
	// block, so stream it straight through.
	if priority := conda.CondarcChannels(overlayPath, envPrefix); len(priority) > 0 && !explicit {
		var buf bytes.Buffer
		if err := exec.Run(cmd.Context(), opts, exec.IO{Stdout: &buf, Stderr: stderr}); err != nil {
			return fmt.Errorf("failed to export environment: %w", err)
		}
		_, err := out.Write(reorderChannelBlock(buf.Bytes(), priority))
		return err
	}

	if err := exec.Run(cmd.Context(), opts, exec.IO{Stdout: out, Stderr: stderr}); err != nil {
		return fmt.Errorf("failed to export environment: %w", err)
	}
	return nil
}
