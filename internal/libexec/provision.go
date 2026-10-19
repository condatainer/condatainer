package libexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

// ensureMu serializes EnsureMicromamba callers in this process.
var ensureMu sync.Mutex

// updateLockName is the lock inside the toolchain that serializes Update calls.
const updateLockName = ".libexec.lock"

// Where the standalone micromamba binaries are published, replaceable by tests.
//   - micromambaAPI describes the releases, and micromambaDownloads serves their files.
var (
	micromambaAPI       = "https://api.github.com/repos/mamba-org/micromamba-releases/releases"
	micromambaDownloads = "https://github.com/mamba-org/micromamba-releases/releases"
)

// packages lists what Update can install, in install order.
var packages = []string{PackageMicromamba, PackageApptainer}

// floor is the minimum version of a tool, and what that version buys.
type floor struct {
	major, minor int
	need         string
}

// floors maps a tool to the oldest version CondaTainer can use.
var floors = map[string]floor{
	"apptainer":  {1, 4, "zstd support"},
	"mksquashfs": {4, 4, "zstd and -offset support"},
	"unsquashfs": {4, 4, "zstd and -offset support"},
}

// Update installs or updates the named packages, or both with no names, under an exclusive lock.
//   - A package is named as name or name@version. The version may be partial: apptainer@1.5 is the newest 1.5.x.
//   - micromamba is a standalone binary from its own release.
//   - apptainer is the bundle: apptainer and the tools it and CondaTainer use.
func Update(ctx context.Context, tools ...string) error {
	want, err := parseSpecs(tools)
	if err != nil {
		return err
	}
	return run(ctx, want, false)
}

// EnsureMicromamba installs micromamba when the toolchain has none, and does
// nothing otherwise. Callers are serialized in this process and, through the
// update lock, across processes: one that loses the race finds it installed.
func EnsureMicromamba(ctx context.Context) error {
	ensureMu.Lock()
	defer ensureMu.Unlock()
	if Installed(PackageMicromamba) {
		return nil
	}
	return run(ctx, []spec{{name: PackageMicromamba}}, true)
}

// run installs the packages in want. ifMissing returns once the update lock is
// held if micromamba is by then installed.
func run(ctx context.Context, want []spec, ifMissing bool) error {
	target, err := prepareTarget()
	if err != nil {
		return err
	}
	lock, err := acquireUpdateLock(filepath.Join(target, updateLockName))
	if err != nil {
		return err
	}
	defer lock.Close()
	if ifMissing && Installed(PackageMicromamba) {
		return nil
	}
	for _, w := range want {
		switch w.name {
		case PackageMicromamba:
			err = installMicromamba(ctx, target, w.version)
		case PackageApptainer:
			err = installBundle(ctx, target, w.version)
		}
		if err != nil {
			return fmt.Errorf("failed to install %s: %w", w.name, err)
		}
	}
	return nil
}

// prepareTarget creates the toolchain directory and returns it with symlinks resolved.
//   - A directory holding anything but a toolchain, or the leftovers of an update, is refused, so a mistyped CNT_LIBEXEC is never written into.
func prepareTarget() (string, error) {
	target := config.GetLibexecDir()
	if target == "" {
		return "", fmt.Errorf("no location for the toolchain; set CNT_LIBEXEC")
	}
	if entries, err := os.ReadDir(target); err == nil && !isProvisioned(target) {
		for _, e := range entries {
			if e.Name() != updateLockName && e.Name() != stagingName {
				return "", fmt.Errorf("%s exists and is not a toolchain; remove it or set CNT_LIBEXEC elsewhere", target)
			}
		}
	}
	if err := utils.MkdirAllShared(target); err != nil {
		return "", notWritable(target, err)
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %w", target, err)
	}
	return real, nil
}

// acquireUpdateLock takes the exclusive update lock, creating its file.
//   - Only a held lock is reported as another update.
//   - A lock that cannot be created or opened for writing is reported as not writable.
func acquireUpdateLock(path string) (*utils.FileLock, error) {
	if !utils.FileExists(path) {
		f, err := utils.CreateFileWritable(path)
		if err != nil {
			return nil, notWritable(filepath.Dir(path), err)
		}
		f.Close()
	}
	lock, err := utils.AcquireFileLock(path, true)
	if errors.Is(err, utils.ErrLockConflict) {
		return nil, fmt.Errorf("another condatainer update of the toolchain is running: %w", err)
	}
	if err != nil {
		return nil, notWritable(filepath.Dir(path), err)
	}
	return lock, nil
}

// installMicromamba downloads the standalone micromamba for this platform to bin/micromamba.
// version is empty for the newest release, or a full or partial version.
//   - The binary is checked against its published sha256 before it replaces the installed one.
func installMicromamba(ctx context.Context, target, version string) error {
	asset, err := micromambaAssetName()
	if err != nil {
		return err
	}
	base := micromambaDownloads + "/latest/download/"
	if version != "" {
		rels, err := listReleases(ctx, micromambaAPI)
		if err != nil {
			return err
		}
		tags := make([]string, len(rels))
		for i, r := range rels {
			tags[i] = r.Tag
		}
		tag, err := pickTag(tags, version)
		if err != nil {
			return fmt.Errorf("micromamba: %w", err)
		}
		base = micromambaDownloads + "/download/" + tag + "/"
	}

	bin := filepath.Join(target, "bin")
	if err := makeDir(bin); err != nil {
		return notWritable(target, err)
	}
	dest := filepath.Join(bin, PackageMicromamba)
	next, sum := dest+".new", dest+".sha256"
	defer os.Remove(next) //nolint:errcheck
	defer os.Remove(sum)  //nolint:errcheck
	if err := utils.DownloadFile(ctx, base+asset, next); err != nil {
		return fmt.Errorf("failed to download micromamba: %w", err)
	}
	if err := utils.DownloadFile(ctx, base+asset+".sha256", sum); err != nil {
		return fmt.Errorf("failed to download the micromamba checksum: %w", err)
	}
	if err := verifyChecksum(next, sum); err != nil {
		return err
	}
	if err := os.Chmod(next, 0o755); err != nil {
		return err
	}
	utils.ShareWithParentGroup(next)
	return os.Rename(next, dest)
}

// micromambaAssetName maps the running OS/arch to the standalone release
// asset name mamba-org/micromamba-releases publishes. Apptainer itself is
// Linux-only, so this tool has no reason to support anything else.
func micromambaAssetName() (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("the self-provisioned toolchain is only available on linux (GOOS=%s)", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		return "micromamba-linux-64", nil
	case "arm64":
		return "micromamba-linux-aarch64", nil
	default:
		return "", fmt.Errorf("no micromamba release for linux/%s", runtime.GOARCH)
	}
}

// versionFlag is the flag a binary prints its own version for.
// squashfs-tools (mksquashfs, unsquashfs) predates GNU-style long options and
// only recognizes the single-dash form; everything else takes "--version".
func versionFlag(name string) string {
	switch name {
	case "mksquashfs", "unsquashfs":
		return "-version"
	default:
		return "--version"
	}
}

// CheckFloor checks the binary at path against the minimum version CondaTainer
// needs of the tool called name. A name with no floor passes.
func CheckFloor(ctx context.Context, path, name string) error {
	f, ok := floors[name]
	if !ok {
		return nil
	}
	// Exit status is ignored: unsquashfs prints its version and exits 1.
	out, runErr := exec.CommandContext(ctx, path, versionFlag(name)).CombinedOutput()
	version := parseVersion(string(out))
	if version == "" {
		if runErr != nil {
			return fmt.Errorf("could not read %s's version: %w", name, runErr)
		}
		return fmt.Errorf("could not parse %s's version from %q", name, strings.TrimSpace(string(out)))
	}
	if !meetsFloor(version, f.major, f.minor) {
		return fmt.Errorf("%s %s is below the required %d.%d (%s)", name, version, f.major, f.minor, f.need)
	}
	return nil
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)(\.\d+)?`)

func parseVersion(output string) string {
	return versionPattern.FindString(strings.TrimSpace(output))
}

// ToolVersion is one provisioned binary's name and the version its own
// version flag (see versionFlag) reported.
type ToolVersion struct {
	Name    string
	Version string
}

// reported lists the binaries whose version Versions shows.
var reported = []string{PackageMicromamba, PackageApptainer, "fuse2fs"}

// Versions runs each installed reported binary's own version flag. A binary
// that fails to run or prints nothing parseVersion recognizes reports
// "unknown" rather than failing the rest.
func Versions(ctx context.Context) ([]ToolVersion, error) {
	if _, ok := Dir(); !ok {
		return nil, ErrNotProvisioned
	}
	var versions []ToolVersion
	for _, name := range reported {
		path, ok := Path(name)
		if !ok {
			continue
		}
		out, _ := exec.CommandContext(ctx, path, versionFlag(name)).CombinedOutput()
		version := parseVersion(string(out))
		if version == "" {
			version = "unknown"
		}
		versions = append(versions, ToolVersion{Name: name, Version: version})
	}
	return versions, nil
}

// meetsFloor reports whether version is at or above floorMajor.floorMinor.
func meetsFloor(version string, floorMajor, floorMinor int) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	return major > floorMajor || (major == floorMajor && minor >= floorMinor)
}
