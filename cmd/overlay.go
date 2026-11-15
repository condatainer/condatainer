package cmd

import (
	"strings"

	"github.com/condatainer/condatainer/internal/image/ext3"
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
