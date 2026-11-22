// Package testenv isolates a test binary from the CondaTainer installation and
// configuration of the machine it runs on.
package testenv

import (
	"os"
	"path/filepath"
	"strings"
)

// libexecToolDirs are the directories under the toolchain that hold executables.
var libexecToolDirs = []string{"bin", filepath.Join("lib", "apptainer", "bin")}

// LibexecOnPath puts the toolchain's tool directories first on PATH, so a test that runs mksquashfs, unsquashfs or fuse-overlayfs gets the provisioned copy before any other.
//   - The toolchain is CNT_LIBEXEC, else <CNT_ROOT>/libexec, as the environment names them when it is called.
//   - A directory that does not exist is skipped, and with none the PATH is unchanged.
//   - Isolate calls it before it clears CNT_*. A test binary that does not isolate calls it from TestMain.
func LibexecOnPath() {
	dir := os.ExpandEnv(os.Getenv("CNT_LIBEXEC"))
	if dir == "" {
		if root := os.Getenv("CNT_ROOT"); root != "" {
			dir = filepath.Join(root, "libexec")
		}
	}
	if dir == "" {
		return
	}
	var tools []string
	for _, sub := range libexecToolDirs {
		if info, err := os.Stat(filepath.Join(dir, sub)); err == nil && info.IsDir() {
			tools = append(tools, filepath.Join(dir, sub))
		}
	}
	if len(tools) == 0 {
		return
	}
	os.Setenv("PATH", strings.Join(append(tools, os.Getenv("PATH")), string(os.PathListSeparator)))
}

// Isolate points HOME and every data root at a fresh temporary directory, clears every CNT_* variable, and fixes default_distro, which every command requires.
//   - It puts the toolchain's tools on PATH first (LibexecOnPath), since the isolated root has none.
//   - It returns a func that removes the directory.
//   - Call it from TestMain before any test runs: config caches the installation root on first use, so a later t.Setenv has no effect.
func Isolate() (cleanup func()) {
	LibexecOnPath()
	dir, err := os.MkdirTemp("", "condatainer-test-")
	if err != nil {
		panic(err)
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "CNT_") {
			os.Unsetenv(name)
		}
	}
	for name, value := range map[string]string{
		"HOME":                      dir,
		"CNT_ROOT":                  dir,
		"CNT_CONFIG_DEFAULT_DISTRO": "ubuntu24",
		"SCRATCH":                   "",
		"XDG_DATA_HOME":             "",
		"XDG_CONFIG_HOME":           "",
	} {
		os.Setenv(name, value)
	}
	return func() { os.RemoveAll(dir) }
}
