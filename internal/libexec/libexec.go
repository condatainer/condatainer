// Package libexec resolves and provisions CondaTainer's self-provisioned
// toolchain — micromamba plus, on request, mksquashfs, squashfuse, fuse-overlayfs
// and an ordinary (non-fakeroot) apptainer, installed via micromamba into one
// directory.
package libexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/utils"
)

// binMarker is the executable that must exist for libexec/ to count as
// provisioned, rather than an empty or half-written directory. micromamba
// is the one package every prefix holds.
const binMarker = "micromamba"

// ErrNotProvisioned is what a container-bound caller returns when the
// toolchain is not provisioned. Condatainer never auto-provisions on its own — an
// ordinary exec/run or a build must never trigger a first-time download — so
// every message directs the user to the one command that does.
var ErrNotProvisioned = errors.New("the self-provisioned toolchain is not installed; run `condatainer update --libexec` first")

// NotProvisionedMessage explains why name could not be found, for a generated shell script's own failure message, where a Go error cannot reach.
//   - It names the install command only when this package installs name. Pointing someone at `condatainer update --libexec` for e2fsprogs would not help.
//   - The text is single-quoted, not backtick-quoted like ErrNotProvisioned's.
//   - It sits inside a double-quoted shell echo, where a backtick would attempt command substitution.
func NotProvisionedMessage(name string) string {
	if p, ok := packageFor(name); ok {
		return fmt.Sprintf("%s not found; run 'condatainer update --libexec %s' to install it", name, p.name)
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
	p, ok := packageFor(name)
	if !ok {
		return fmt.Errorf("%s is not part of the self-provisioned toolchain", name)
	}
	return fmt.Errorf("%s is not installed in the self-provisioned toolchain; run `condatainer update --libexec %s`", name, p.name)
}

// Dir returns the toolchain directory resolved to its real (symlink-free) path,
// and true when it is provisioned (has bin/micromamba). Returns "", false otherwise.
func Dir() (string, bool) {
	dir := config.GetLibexecDir()
	if dir == "" {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", binMarker)); err != nil {
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

// ApptainerPath returns the path to the self-provisioned apptainer binary,
// the one ordinary exec/run and conda/script builds use — never the
// fakeroot-capable system/module one an os/base .def build needs.
func ApptainerPath() (string, bool) {
	return Path("apptainer")
}

// MicromambaPath returns the path to the self-provisioned micromamba binary.
func MicromambaPath() (string, bool) {
	return Path("micromamba")
}

// Path returns the path of the binary called name in the provisioned bin/,
// and true only if it is installed there. internal/toolpath.Resolve calls this
// for an arbitrary name, and a name this package never provisions is simply
// not found.
func Path(name string) (string, bool) {
	bin, ok := BinDir()
	if !ok {
		return "", false
	}
	path := filepath.Join(bin, name)
	if !utils.FileExists(path) {
		return "", false
	}
	return path, true
}

// Installed reports whether the provisioned toolchain has the binary name.
func Installed(name string) bool {
	_, ok := Path(name)
	return ok
}
