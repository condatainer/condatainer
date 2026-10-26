package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
)

// projectContext is a resolved project run: what to mount, and where the job
// must run. Nil when the command was not run from a project root.
type projectContext struct {
	Root string
	// Overlays are absolute paths, in declaration order.
	Overlays []string
}

// projectRunContext resolves a script's declarations through its project lock, or reports that there is no project to resolve against.
//   - list prints the resolved overlays. A run that is about to submit a job leaves it to the job, whose log keeps it.
//   - A script the project ignores resolves as outside the project: nothing is pinned.
//   - The project is the nearest ancestor of the current directory holding cnt-lock/.
//   - The script may live anywhere: declarations resolve against the project root.
//   - The working directory is left to the script and the scheduler.
//   - A #DEP: with no pin resolves live, as outside a project.
func projectRunContext(ctx context.Context, contentScript string, list bool) (*projectContext, error) {
	if noProjectRequested {
		return nil, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	standing, err := project.StandingAt(cwd)
	if err != nil || standing == nil {
		return nil, err
	}
	script, err := filepath.Abs(contentScript)
	if err != nil {
		return nil, err
	}
	outside, err := outsideProject(standing.Root, script)
	if err != nil {
		return nil, err
	}
	if outside {
		announceUnlocked(standing.Root, script)
		return nil, nil
	}
	announceProject(standing.Root)

	scanned, err := lock.ScanScript(standing.Root, script)
	if err != nil {
		return nil, err
	}
	resolution, err := standing.ResolveComplete(ctx, scanned.Requests,
		project.ResolveOptions{LiveResolve: true, Distro: projectDefaultDistro()})
	if err != nil {
		return nil, err
	}

	projectRun := &projectContext{Root: standing.Root}
	for _, mount := range resolution.Mounts {
		projectRun.Overlays = append(projectRun.Overlays, mount.Path)
	}
	if list {
		printMounts(resolution.Mounts, true)
	}
	return projectRun, nil
}

// outsideProject reports whether the project's lock does not cover a script.
//   - The project's rules can skip it, or a symlink on its path can lead outside the project or to a file the rules skip.
//   - A symlink to a script of the project is covered, since the scan reads that script under its own name.
func outsideProject(root, script string) (bool, error) {
	member, err := lock.IsMember(root, script)
	if err != nil || !member {
		return !member && err == nil, err
	}
	realScript, err := filepath.EvalSymlinks(script)
	if err != nil {
		return false, nil
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, nil
	}
	if rel, err := filepath.Rel(root, script); err == nil && realScript == filepath.Join(realRoot, rel) {
		return false, nil
	}
	member, err = lock.IsMember(realRoot, realScript)
	return !member || err != nil, nil
}

// announceUnlocked prints the project line for a script the lock does not cover, marked, when the script declares anything to resolve.
func announceUnlocked(root, script string) {
	if deps, _ := utils.ScriptDependencies(script); len(deps) > 0 {
		announceProjectMarked(root, "script not locked")
	}
}
