package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/image/ext3"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var overlayCreateCmd = &cobra.Command{
	Use:   "create [flags] [path] [-- packages...]",
	Short: "Create a new ext3 overlay",
	Long:  overlayCreateHelp,
	Example: `  condatainer overlay create                           # env.img, 10G
  condatainer overlay create my_data.img -s 50g -p large
  condatainer overlay create --fakeroot --sparse
  condatainer overlay create -f environment.yml
  condatainer overlay create myenv.img -- python=3.11  # With Conda packages`,

	Args: cobra.ArbitraryArgs,

	Run: runOverlayCreate,
}

// overlayCreateHelp is the help body for 'overlay create'.
const overlayCreateHelp = `Create an ext3 overlay, sized and tuned by profile (-p).

- The default path is env.img. Supports both .img and .ext3 extensions.
- Conda packages after -- or an environment file (-f) set up Conda right away.
- Without either, Conda is set up on the first install.`

func init() {
	registerOverlayCreateFlags(overlayCreateCmd)
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

// overlayProfileFlag returns the chosen overlay profile.
func overlayProfileFlag(cmd *cobra.Command) string {
	v, _ := cmd.Flags().GetString("profile")
	return v
}

// runOverlayCreate implements 'overlay create'. It resolves the target path, parses the flags registered by
// registerOverlayCreateFlags, then builds the overlay with exec.CreateCondaOverlay.
func runOverlayCreate(cmd *cobra.Command, args []string) {
	// 1. Split args at -- into image path and packages
	var packages []string
	if dashAt := cmd.ArgsLenAtDash(); dashAt >= 0 {
		packages = args[dashAt:]
		args = args[:dashAt]
	}

	// Handle Positional Argument (Image Path)
	path := "env.img"
	if len(args) > 0 {
		path = args[0]
	} else if len(args) > 1 {
		ExitWithError("Too many positional arguments before --.")
	}

	// Auto-append .img extension if not present and path doesn't have an extension
	if !utils.IsImg(path) {
		if strings.Contains(filepath.Base(path), ".") {
			ExitWithError("Overlay must have a .img or .ext3 extension.")
		}
		path += ".img"
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		ExitWithError("Failed to resolve path: %v", err)
	}
	path = absPath

	if utils.FileExists(path) || utils.DirExists(path) {
		ExitWithError("Path %s already exists.", path)
	}

	// lookup.Path is always beside path (LookupSnapshot's own invariant), so
	// only its filename is shown — path's directory already covers it.
	if lookup := container.LookupSnapshot(path); lookup.Path != "" {
		utils.PrintMessage("Found paired snapshot %s — %s will autoload it",
			filepath.Base(lookup.Path), displayPath(path))
	}

	// 2. Parse Flags
	sizeStr, _ := cmd.Flags().GetString("size")
	fakeroot, _ := cmd.Flags().GetBool("fakeroot")
	sparse, _ := cmd.Flags().GetBool("sparse")
	profile := overlayProfileFlag(cmd)
	envFile, _ := cmd.Flags().GetString("file")

	if envFile != "" && len(packages) > 0 {
		ExitWithError("Cannot use -f/--file and inline packages (--) at the same time.")
	}

	sizeMB, err := utils.ParseSizeToMB(sizeStr)
	if err != nil {
		ExitWithError("Invalid size format '%s': %v", sizeStr, err)
	}

	// Unknown profile names fall back to balanced, but warn so a typo is visible.
	resolvedProfile, err := ext3.ParseProfile(profile)
	if err != nil {
		utils.PrintWarning("Unknown profile %q, using 'balanced'. Valid: %s",
			profile, strings.Join(overlayProfiles, ", "))
		resolvedProfile = ext3.ProfileDefault
	}

	uid, gid := os.Getuid(), os.Getgid()
	if fakeroot {
		uid, gid = 0, 0
	}
	opts := &ext3.CreateOptions{
		Path:           path,
		SizeMB:         sizeMB,
		UID:            uid,
		GID:            gid,
		Profile:        resolvedProfile,
		Sparse:         sparse,
		FilesystemType: "ext3",
	}

	pkgs, err := overlayInitPackages(envFile, packages)
	if err != nil {
		ExitWithError("%v", err)
	}
	if len(pkgs) > 0 {
		// The install runs inside the default root, which exec finds but cannot build.
		if _, err := ensureRootBaseImage(cmd.Context(), nil); err != nil {
			ExitWithError("%v", err)
		}
		describeOverlayInit(path, envFile, packages)
	}

	condaIO := exec.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if err := exec.CreateCondaOverlay(cmd.Context(), opts, pkgs, "", fakeroot, condaIO); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(cmd.Context().Err(), context.Canceled) {
			utils.PrintWarning("Overlay creation cancelled.")
			return
		}
		ExitWithError("Failed to create overlay: %v", err)
	}

	utils.PrintSuccess("Created overlay %s", displayPath(path))
}

// overlayInitPackages is the install request for a new overlay: the arguments
// for 'condatainer env install', or nil when Conda is set up on first install.
// An environment file is checked to exist and to be a Conda file.
func overlayInitPackages(envFile string, packages []string) ([]string, error) {
	if envFile == "" {
		return packages, nil
	}
	absEnvFile, err := filepath.Abs(envFile)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path of environment file: %w", err)
	}
	if !utils.FileExists(absEnvFile) {
		return nil, fmt.Errorf("environment file %s not found", absEnvFile)
	}
	if !utils.IsCondaFile(absEnvFile) {
		return nil, fmt.Errorf("environment file must be .yml/.yaml or an explicit spec file (.txt)")
	}
	return []string{"-f", absEnvFile}, nil
}

// describeOverlayInit announces what a new overlay at path will install.
func describeOverlayInit(path, envFile string, packages []string) {
	what := strings.Join(packages, " ")
	if envFile != "" {
		what = displayPath(envFile)
	}
	if container.LookupSnapshot(path).Path != "" {
		utils.PrintMessage("Installing %s on top of the paired snapshot...", what)
		return
	}
	utils.PrintMessage("Initializing conda environment with: %s...", what)
}
