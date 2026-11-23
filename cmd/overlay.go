package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/image/ext3"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

// overlayCmd is the parent command for overlay management
var overlayCmd = &cobra.Command{
	Use:   "overlay",
	Short: "Manage writable ext3 overlays",
	Long: `Manage writable ext3 overlays.

- Create a new overlay, or resize an existing one.
- Check filesystem integrity, or inspect usage.
- Export its Conda environment.
- Freeze it into an immutable .sqf, or unfreeze one back.`,
}

// overlayProfiles are the selectable filesystem tuning profiles for a new image.
var overlayProfiles = []string{"small", "balanced", "large"}

func init() {
	rootCmd.AddCommand(overlayCmd)
	overlayCmd.AddCommand(overlayCreateCmd, resizeCmd, infoCmd, fscheckCmd, chownCmd, exportCmd,
		overlayFreezeCmd, overlayUnfreezeCmd)
}

// registerOverlayCreateFlags registers the full flag set for creating an overlay.
// --profile matches image/ext3's Profile vocabulary.
func registerOverlayCreateFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("size", "s", ext3.DefaultSize, "Set overlay size (e.g., 500M, 10g)")
	cmd.Flags().StringP("profile", "p", "balanced", "Overlay profile: small/balanced/large files")
	cmd.Flags().Bool("fakeroot", false, "Create a fakeroot-compatible overlay (owned by root)")
	cmd.Flags().BoolP("sparse", "S", false, "Create a sparse overlay (no pre-allocation)")
	cmd.Flags().StringP("file", "f", "", "Initialize with a Conda environment file (.yml/.yaml) or explicit spec (.txt)")

	cmd.RegisterFlagCompletionFunc("profile", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		res := make([]string, 0, len(overlayProfiles))
		for _, o := range overlayProfiles {
			if toComplete == "" || strings.HasPrefix(o, toComplete) {
				res = append(res, o)
			}
		}
		return res, cobra.ShellCompDirectiveNoFileComp
	}) //nolint:errcheck

	cmd.RegisterFlagCompletionFunc("file", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterFileExt
	}) //nolint:errcheck
}

// completeImages tells the shell to suggest ext3 overlay files (.img, .ext3).
func completeImages(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{"img", "ext3"}, cobra.ShellCompDirectiveFilterFileExt
}

// displayPath is p relative to cwd when possible, so a message about a plain
// "env.img" typed on the command line doesn't balloon into a long absolute
// path — matches the display convention in checkDeps.
func displayPath(p string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}

// resolveOverlayPath turns a path typed for a writable overlay into the absolute path it names.
//   - A path ending in .img or .ext3 is kept. One with no extension gets defaultExt. Any other extension is refused.
//   - A path that is empty or ends in a separator names no file.
//   - An existing file or directory at the path is refused.
//   - The folder it sits in need not exist: makeOverlayParent creates it.
func resolveOverlayPath(name, defaultExt string) (string, error) {
	if name == "" || strings.HasSuffix(name, string(filepath.Separator)) {
		return "", errors.New("give the overlay a path, ending in .img or .ext3 or with no extension")
	}
	if !utils.IsImg(name) {
		if strings.Contains(filepath.Base(name), ".") {
			return "", fmt.Errorf("%s is not a writable overlay path; it ends in .img or .ext3, or has no extension", name)
		}
		name += defaultExt
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if utils.FileExists(abs) || utils.DirExists(abs) {
		return "", fmt.Errorf("%s already exists", displayPath(abs))
	}
	return abs, nil
}

// makeOverlayParent creates the folder that holds the overlay at path.
func makeOverlayParent(path string) error {
	return utils.MkdirAllShared(filepath.Dir(path))
}

// refusePairing refuses an overlay that would load together with snapshot, for an overlay that holds everything the snapshot holds.
func refusePairing(path, snapshot string) error {
	if container.PairsWith(path, snapshot) {
		return fmt.Errorf("%s would pair with %s and load on top of it; choose another path", displayPath(path), displayPath(snapshot))
	}
	return nil
}
