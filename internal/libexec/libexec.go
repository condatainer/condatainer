// Package libexec resolves and provisions CondaTainer's self-provisioned
// toolchain: micromamba, and the Apptainer bundle (apptainer plus the tools it
// and CondaTainer use), unpacked into one directory.
package libexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

// Names accepted by Update, and what each installs.
const (
	PackageApptainer  = "apptainer"
	PackageMicromamba = "micromamba"
)

// manifestName is the bundle's list of files, kept at the toolchain root.
const manifestName = "MANIFEST.json"

// bundleTools lists the binaries the Apptainer bundle provides.
var bundleTools = []string{
	"apptainer", "mksquashfs", "unsquashfs", "squashfuse_ll", "fuse-overlayfs", "fusermount3",
	"fuse2fs", "mke2fs", "e2fsck", "tune2fs", "debugfs", "resize2fs",
}

// toolDirs are the directories under the toolchain that hold executables, in search order.
var toolDirs = []string{"bin", filepath.Join("lib", "apptainer", "bin")}

// ErrNotProvisioned is what a container-bound caller returns when the
// toolchain is not provisioned. Condatainer never auto-provisions on its own — an
// ordinary exec/run or a build must never trigger a first-time download — so
// every message directs the user to the one command that does.
var ErrNotProvisioned = errors.New("the self-provisioned toolchain is not installed; run `condatainer update --libexec` first")

// providedBy names the package that installs the binary called name, or "".
func providedBy(name string) string {
	switch {
	case name == PackageMicromamba:
		return PackageMicromamba
	case slices.Contains(bundleTools, name):
		return PackageApptainer
	}
	return ""
}

// NotProvisionedMessage explains why name could not be found, for a generated shell script's own failure message, where a Go error cannot reach.
//   - It names the install command only when this package installs name.
//   - The text is single-quoted, not backtick-quoted like ErrNotProvisioned's.
//   - It sits inside a double-quoted shell echo, where a backtick would attempt command substitution.
func NotProvisionedMessage(name string) string {
	if p := providedBy(name); p != "" {
		return fmt.Sprintf("%s not found; run 'condatainer update --libexec %s' to install it", name, p)
	}
	return name + " not found"
}

// NotInstalledError is what a container-bound caller returns when name is not
// in the toolchain: ErrNotProvisioned when the toolchain is not provisioned,
// otherwise a message naming the package that installs it.
func NotInstalledError(name string) error {
	if _, ok := Dir(); !ok {
		return ErrNotProvisioned
	}
	p := providedBy(name)
	if p == "" {
		return fmt.Errorf("%s is not part of the self-provisioned toolchain", name)
	}
	return fmt.Errorf("%s is not installed in the self-provisioned toolchain; run `condatainer update --libexec %s`", name, p)
}

// isProvisioned reports whether dir holds an installed micromamba or bundle.
func isProvisioned(dir string) bool {
	return utils.FileExists(filepath.Join(dir, "bin", PackageMicromamba)) || utils.FileExists(filepath.Join(dir, manifestName))
}

// Dir returns the toolchain directory resolved to its real (symlink-free) path,
// and true when it is provisioned. Returns "", false otherwise.
func Dir() (string, bool) {
	dir := config.GetLibexecDir()
	if dir == "" || !isProvisioned(dir) {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir, true
}

// BinDir returns the provisioned toolchain's bin/ directory.
func BinDir() (string, bool) {
	dir, ok := Dir()
	if !ok {
		return "", false
	}
	return filepath.Join(dir, "bin"), true
}

// ApptainerPath returns the path to the self-provisioned apptainer binary.
func ApptainerPath() (string, bool) {
	return Path(PackageApptainer)
}

// MicromambaPath returns the path to the self-provisioned micromamba binary.
func MicromambaPath() (string, bool) {
	return Path(PackageMicromamba)
}

// Path returns the path of the binary called name in the provisioned toolchain,
// and true only if it is installed there. It looks in bin/, then in the
// directory Apptainer keeps its own helpers in.
// internal/toolpath.Resolve calls this for an arbitrary name, and a name this
// package never provisions is simply not found.
func Path(name string) (string, bool) {
	dir, ok := Dir()
	if !ok {
		return "", false
	}
	for _, sub := range toolDirs {
		path := filepath.Join(dir, sub, name)
		if utils.FileExists(path) {
			return path, true
		}
	}
	return "", false
}

// Installed reports whether the provisioned toolchain has the binary name.
func Installed(name string) bool {
	_, ok := Path(name)
	return ok
}

// notWritable reports that dir cannot be written.
func notWritable(dir string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%s is not writable", dir)
	}
	return fmt.Errorf("cannot prepare the toolchain in %s: %w", dir, err)
}
