// Package project holds what a project is, above the lock that records it.
package project

import (
	"fmt"
	"path/filepath"
)

// WorkDir is the directory a submitted restore job must run in, given whatever working directory its recipe declared.
//   - An empty declared value means none was given.
//   - The answer is always the project root, and a declared directory that is not the root is refused.
//   - A relative path in a project means the root, so a job running elsewhere would split the overlays from the script's own paths.
//   - It refuses rather than overrides: only the author can resolve a declared `--chdir`.
func WorkDir(root, declared string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if declared == "" {
		return root, nil
	}
	absolute, err := filepath.Abs(declared)
	if err != nil {
		return "", err
	}
	if !samePath(absolute, root) {
		return "", fmt.Errorf("the script runs in %s but this project is rooted at %s; a project's relative paths resolve against the root",
			absolute, root)
	}
	return root, nil
}

// samePath compares two absolute paths, resolving symlinks when both exist.
// A path that cannot be resolved is compared as written, which is the same
// answer for everything except a link, and a link that is not there yet cannot
// be followed anyway.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && resolvedA == resolvedB
}
