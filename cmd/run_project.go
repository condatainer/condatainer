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
//   - A script the project ignores resolves as outside the project: nothing is pinned.
//   - The project is the nearest ancestor of the current directory holding cnt-lock/.
//   - The script may live anywhere: declarations resolve against the project root.
//   - The working directory is left to the script and the scheduler.
//   - A #DEP: with no pin resolves live, as outside a project.
func projectRunContext(ctx context.Context, contentScript string) (*projectContext, error) {
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
	if outside, err := outsideProject(standing.Root, script); outside || err != nil {
		return nil, err
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
		switch {
		case mount.Found != "":
			utils.PrintNote("%s is mounted from an equivalent overlay, not %s",
				mount.Name, short(mount.Identity))
		case mount.Live:
			utils.PrintNote("%s resolved to %s, not pinned",
				mount.Request, mount.Name)
		}
	}
	return projectRun, nil
}

// outsideProject reports whether a script is left out of the project's lock, and says so when it is.
func outsideProject(root, script string) (bool, error) {
	member, err := lock.IsMember(root, script)
	if err != nil || member {
		return false, err
	}
	utils.PrintNote("%s is ignored by the project, overlays resolved without lock", script)
	return true, nil
}
