package utils

import (
	"os"
	"path/filepath"
)

// EnsureTmpSubdir creates the leaf directory dir. The parent must already exist.
//   - An existing directory is accepted, so concurrent builds are safe.
//   - Under a world-writable parent such as /tmp the mode is 0700, so other users cannot read or list it.
//   - Otherwise it uses PermDir and ShareWithParentGroup, so a group-writable parent passes group-write down.
func EnsureTmpSubdir(dir string) error {
	if info, err := os.Stat(filepath.Dir(dir)); err == nil && info.Mode()&0002 != 0 {
		// Parent world-writable: keep the scratch private, don't share.
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		return nil
	}
	if err := os.Mkdir(dir, PermDir); err != nil && !os.IsExist(err) {
		return err
	}
	ShareWithParentGroup(dir)
	return nil
}

// GetTmpDir returns the tmp directory for builds, from the first of these that is set:
//  1. CNT_TMPDIR
//  2. the scheduler's node-local storage: SLURM_TMPDIR, PBS_TMPDIR, LSF_TMPDIR, _CONDOR_SCRATCH_DIR
//  3. TMPDIR
//  4. /tmp
//
// cnt-$USER is appended so users do not collide.
func GetTmpDir() string {
	user := os.Getenv("USER")
	if user == "" {
		user = "condatainer"
	}
	sub := "cnt-" + user

	// Priority 1: explicit override
	if v := os.Getenv("CNT_TMPDIR"); v != "" {
		return filepath.Join(v, sub)
	}

	// Priority 2: scheduler-assigned local node storage (fast per-job scratch)
	for _, env := range []string{"SLURM_TMPDIR", "PBS_TMPDIR", "LSF_TMPDIR", "_CONDOR_SCRATCH_DIR"} {
		if v := os.Getenv(env); v != "" {
			return filepath.Join(v, sub)
		}
	}

	// Priority 3: POSIX standard tmp dir
	if v := os.Getenv("TMPDIR"); v != "" {
		return filepath.Join(v, sub)
	}

	return filepath.Join("/tmp", sub)
}
