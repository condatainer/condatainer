package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/helper"
	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

// ── update command ──────────────────────────────────────────────────────────

var (
	updateLibexec bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Refresh recipe and helper caches, or update the toolchain",
	Long: `Refresh the recipe and helper script caches, or update the toolchain.

- With no flags, both caches are refreshed.

--libexec updates the toolchain instead.
  Tools: apptainer, squashfs-tools, squashfuse, fuse-overlayfs.
  Writes to $CNT_ROOT/libexec, or $CNT_LIBEXEC when set.`,
	Example: `  condatainer update                       # Refresh recipe and helper caches
  condatainer update --libexec             # Install missing tools, update the rest
  condatainer update --libexec apptainer   # Install and update apptainer`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && !updateLibexec {
			return fmt.Errorf("tools can only be named with --libexec")
		}
		return nil
	},
	SilenceUsage: true,
	RunE:         runUpdate,
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVar(&updateLibexec, "libexec", false, "Install missing tools and update the toolchain; name tools to install just those")
}

// errLibexecInContainer is why --libexec does nothing inside a container.
var errLibexecInContainer = errors.New("--libexec must be run on the host, not inside a container")

// libexecHostError refuses --libexec inside a container.
func libexecHostError(insideContainer bool) error {
	if insideContainer {
		return errLibexecInContainer
	}
	return nil
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if updateLibexec {
		if err := libexecHostError(config.IsInsideContainer()); err != nil {
			return err
		}
	}

	if !updateLibexec {
		if err := config.RefreshCatalogCache(); err != nil {
			return fmt.Errorf("failed to clear the recipe cache: %w", err)
		}
		cat, err := config.OpenCatalog(cmd.Context())
		if err != nil {
			return fmt.Errorf("failed to open recipe sources: %w", err)
		}
		for _, src := range cat {
			utils.PrintMessage("Fetching recipes from %s (%s) ...", src.Name, src.Base)
		}
		entries := cat.Entries(cmd.Context())
		config.WarnUnreachableSources(cmd.Context(), cat)
		utils.PrintSuccess("%d recipes available.", len(entries))

		if _, err := helper.RefreshRemoteMetadata(cmd.Context(), true, os.Stdout); err != nil {
			utils.PrintWarning("Failed to update helper script metadata: %v", err)
		} else {
			utils.PrintSuccess("Helper script metadata updated.")
		}
	}

	if updateLibexec {
		// libexec.Update holds its own lock and refuses internally if the
		// toolchain is in use; no separate check needed here.
		update := func() error { return libexec.Update(cmd.Context(), args...) }
		if len(args) == 0 {
			// Install what the system lacks, and update what is installed.
			plan, report, warnings := planToolchain(probeSystem(cmd.Context()))
			for _, line := range report {
				utils.PrintMessage("  %s", line)
			}
			for _, line := range warnings {
				utils.PrintWarning("%s", line)
			}
			update = func() error { return libexec.Sync(cmd.Context(), plan...) }
		}
		utils.PrintMessage("Updating the self-provisioned toolchain...")
		if err := update(); err != nil {
			return fmt.Errorf("failed to update the toolchain: %w", err)
		}
		utils.PrintSuccess("Toolchain updated successfully.")
		if versions, err := libexec.Versions(cmd.Context()); err == nil {
			for _, v := range versions {
				utils.PrintMessage("  %-10s %s", v.Name, v.Version)
			}
		}
	}

	return nil
}

// ── self-update command ──────────────────────────────────────────────────────

var (
	selfUpdateForce bool
	selfUpdateDev   bool
)

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Update condatainer to the latest version",
	Long: `Download the latest condatainer from GitHub and replace the current binary.

- No backup of the current version is kept.`,
	Example: `  condatainer self-update        # Update to the latest stable version
  condatainer self-update --yes  # Update without confirmation
  condatainer self-update -f     # Update even if already on the latest version
  condatainer self-update --dev  # Include pre-release versions`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runSelfUpdate,
}

func init() {
	rootCmd.AddCommand(selfUpdateCmd)
	selfUpdateCmd.Flags().BoolVarP(&selfUpdateForce, "force", "f", false, "Force update even if already on latest version")
	selfUpdateCmd.Flags().BoolVar(&selfUpdateDev, "dev", false, "Include pre-release versions")
}

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	// Get current executable path
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlink: %w", err)
	}

	// Detect OS and architecture
	osName := runtime.GOOS
	arch := runtime.GOARCH

	if selfUpdateDev {
		utils.PrintNote("Dev mode enabled, including pre-release versions")
	}

	utils.PrintMessage("Fetching latest release information...")

	// Get release from GitHub API
	var releaseURL string
	if selfUpdateDev {
		// Fetch all releases to find the latest (including pre-releases)
		releaseURL = fmt.Sprintf("https://api.github.com/repos/%s/releases", config.GitHubRepo)
	} else {
		// Fetch only the latest stable release
		releaseURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", config.GitHubRepo)
	}

	resp, err := http.Get(releaseURL)
	if err != nil {
		return fmt.Errorf("failed to fetch release information: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch release: HTTP %d", resp.StatusCode)
	}

	type releaseAsset struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}

	type releaseInfo struct {
		TagName    string         `json:"tag_name"`
		Prerelease bool           `json:"prerelease"`
		Assets     []releaseAsset `json:"assets"`
	}

	var release releaseInfo

	if selfUpdateDev {
		// Parse array of releases and find the latest
		var releases []releaseInfo
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return fmt.Errorf("failed to parse release information: %w", err)
		}

		if len(releases) == 0 {
			return fmt.Errorf("no releases found")
		}

		// The releases are already sorted by creation date (newest first)
		// Take the first one (latest release, stable or pre-release)
		release = releases[0]
	} else {
		// Parse single latest stable release
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return fmt.Errorf("failed to parse release information: %w", err)
		}
	}

	// Check if already on latest version
	currentVersion := "v" + config.Version
	latestVersion := strings.TrimSpace(release.TagName)

	// Compare versions semantically; if parsing fails we assume the
	// latest version is newer (so we update).
	cmp := compareVersions(currentVersion, latestVersion)

	if cmp >= 0 && !selfUpdateForce {
		// Current version >= latest version (equal or newer)
		if cmp == 0 {
			utils.PrintSuccess("Already on the latest version %s!", latestVersion)
		} else {
			utils.PrintSuccess("Already on a newer version %s (latest: %s)",
				currentVersion, latestVersion)
		}
		return nil
	}

	utils.PrintMessage("Current version: %s", currentVersion)
	if release.Prerelease {
		utils.PrintMessage("Latest pre-release: %s", latestVersion)
	} else {
		utils.PrintMessage("Latest version: %s", latestVersion)
	}

	// Ask for confirmation unless global --yes flag or --force flag is provided
	if !utils.ShouldAnswerYes() && !selfUpdateForce {
		fmt.Print("Are you sure to update to the latest version? [y/N]: ")
		confirm, err := utils.ReadLineContext(cmd.Context())
		if err != nil || (confirm != "y" && confirm != "yes") {
			utils.PrintNote("Update cancelled by user.")
			return nil
		}
	}

	// Find matching binary for current OS/arch
	// Expected format: condatainer_{os}_{arch} (e.g., condatainer_linux_amd64)
	binaryName := fmt.Sprintf("condatainer_%s_%s", osName, arch)
	var downloadURL string

	for _, asset := range release.Assets {
		if asset.Name == binaryName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no binary found for %s/%s in release %s", osName, arch, release.TagName)
	}

	utils.PrintMessage("Downloading condatainer %s for %s/%s...", release.TagName, osName, arch)

	// Download to temporary file
	tempPath := fmt.Sprintf("%s.tmp.%d", exePath, os.Getpid())
	if err := downloadFile(downloadURL, tempPath); err != nil {
		return fmt.Errorf("failed to download latest version: %w", err)
	}

	// Make executable
	if err := utils.MakeExecutable(tempPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	// Replace current executable
	// On Unix systems, we can replace the file while it's running
	if err := os.Rename(tempPath, exePath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to replace executable: %w", err)
	}

	utils.PrintSuccess("condatainer updated to %s!", release.TagName)

	return nil
}

// compareVersions compares two semantic versions: -1 if v1 < v2, 0 if equal, 1 if v1 > v2.
//   - Pre-releases follow semver ("1.2.3-alpha" < "1.2.3").
//   - Build metadata is only a secondary lexicographic tie-breaker.
func compareVersions(v1, v2 string) int {
	// semver package requires a leading 'v'; add it if missing so that
	// canonicalization succeeds for numeric-only tags.
	if !strings.HasPrefix(v1, "v") {
		v1 = "v" + v1
	}
	if !strings.HasPrefix(v2, "v") {
		v2 = "v" + v2
	}
	c1 := semver.Canonical(v1)
	c2 := semver.Canonical(v2)
	if c1 == "" || c2 == "" {
		// If we can't parse a version, assume the first is older so an update
		// will be attempted.
		return -1
	}
	res := semver.Compare(c1, c2)
	if res != 0 {
		return res
	}
	b1 := semver.Build(v1)
	b2 := semver.Build(v2)
	if b1 != b2 {
		if b1 < b2 {
			return -1
		}
		return 1
	}
	return 0
}

// downloadFile downloads a file from a URL to a local path
func downloadFile(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Create parent directory
	if err := utils.MkdirAllShared(filepath.Dir(destPath)); err != nil {
		return err
	}

	// Create destination file
	out, err := utils.CreateFileWritable(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Copy data. Permissions were already set by CreateFileWritable (umask-subject,
	// shared with the parent group); io.Copy only writes content.
	_, err = io.Copy(out, resp.Body)
	return err
}
