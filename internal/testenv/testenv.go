// Package testenv isolates a test binary from the CondaTainer installation and
// configuration of the machine it runs on.
package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// Isolate points HOME and every data root at a fresh temporary directory, clears every CNT_* variable and every job variable (ClearJobEnv), and fixes default_distro, which every command requires.
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
	ClearJobEnv()
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

// jobEnvPrefixes start the names of the variables a scheduler sets in a job.
var jobEnvPrefixes = []string{"SLURM_", "PBS_", "LSB_", "_CONDOR_"}

// jobEnvNames are the other variables that say what a job has: the ones PBS and CUDA set, and the resource variables CondaTainer exports, plain and with the CNT_ prefix.
var jobEnvNames = []string{
	"NCPUS", "CUDA_VISIBLE_DEVICES", "OMP_NUM_THREADS",
	"NCPU", "MEM", "MEM_GB", "NNODE", "NRANK", "NRANK_PER_NODE",
	"CNT_NCPU", "CNT_MEM", "CNT_MEM_GB", "CNT_NNODE", "CNT_NRANK", "CNT_NRANK_PER_NODE",
}

// jobEnv lists the names of the job variables in the environment.
func jobEnv() []string {
	var names []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		hit := false
		for _, prefix := range jobEnvPrefixes {
			hit = hit || strings.HasPrefix(name, prefix)
		}
		for _, other := range jobEnvNames {
			hit = hit || name == other
		}
		if hit {
			names = append(names, name)
		}
	}
	return names
}

// ClearJobEnv removes every job variable from the process, so the machine a test runs on, whether a login node, a SLURM, PBS, LSF or HTCondor job or a container in one, does not decide its result.
//   - Isolate calls it. A test binary that does not isolate calls it from TestMain.
func ClearJobEnv() {
	for _, name := range jobEnv() {
		os.Unsetenv(name)
	}
}

// ClearJobEnvFor removes every job variable for one test and puts them back when it ends.
func ClearJobEnvFor(t testing.TB) {
	t.Helper()
	for _, name := range jobEnv() {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}
