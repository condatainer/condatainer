package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/utils"
)

// resolveOverlayValues turns each overlay value (a name, possibly bare or partial, or
// a file, with an optional :ro/:rw suffix) into what the runtime mounts.
//
//   - In a project every value answers to its lock, and a name no pin answers resolves live.
//   - Outside one, a name resolves against what is installed and a file is its own answer.
//   - A version constraint is refused unless allowConstraint, where it is left as written.
//   - An unanswered name or a file is returned unchanged outside a project, and is an error naming `project restore` inside.
func resolveOverlayValues(ctx context.Context, values []string, standing *project.Standing, allowConstraint bool) ([]string, error) {
	requests := make([]lock.Request, 0, len(values))
	suffixes := make([]string, 0, len(values))
	kept := make([]int, 0, len(values))
	out := append([]string(nil), values...)
	for i, value := range values {
		name, suffix := splitOverlayMode(value)
		if name == "" {
			continue
		}
		request, reason := lock.ParseDeclaration(name)
		if reason != "" {
			if dep, err := catalog.ParseDep(name); allowConstraint && err == nil && dep.Op != "" {
				continue
			}
			return nil, fmt.Errorf("%s: %s", name, reason)
		}
		requests = append(requests, request)
		suffixes = append(suffixes, suffix)
		kept = append(kept, i)
	}
	if len(requests) == 0 {
		return out, nil
	}

	opts := project.ResolveOptions{LiveResolve: true, Distro: projectDefaultDistro()}
	var mounts []project.Mount
	if standing != nil {
		resolution, err := standing.ResolveComplete(ctx, requests, opts)
		if err != nil {
			return nil, err
		}
		mounts = resolution.Mounts
	} else {
		var err error
		if mounts, err = project.ResolveUnlocked(ctx, requests, opts); err != nil {
			return nil, err
		}
	}
	if len(mounts) != len(requests) {
		return nil, fmt.Errorf("resolved %d of %d overlays", len(mounts), len(requests))
	}

	for n, mount := range mounts {
		// Outside a project a file or an unanswered name is the resolver's to report.
		if standing == nil && (mount.Unpinned || mount.Path == "") {
			continue
		}
		out[kept[n]] = mount.Path + suffixes[n]
		switch {
		case mount.Found != "":
			utils.PrintNote("%s is mounted from an equivalent artifact, not %s",
				mount.Name, short(mount.Identity))
		case mount.Live && standing != nil:
			utils.PrintNote("%s resolved to %s, not pinned",
				mount.Request, mount.Name)
		case mount.Live && mount.Name != mount.Request:
			utils.PrintNote("Expanding '%s' to '%s'", mount.Request, mount.Name)
		}
	}
	return out, nil
}

// installedOverlayFile resolves an overlay argument the way `exec -o` does: the
// project and catalog step first, then the installed-overlay map that mounts
// use. A file or directory that exists is returned as an absolute path.
func installedOverlayFile(ctx context.Context, arg string) (string, error) {
	resolved, err := projectOverlays(ctx, []string{arg})
	if err != nil {
		return "", err
	}
	abs, _ := filepath.Abs(resolved[0])
	if utils.FileExists(abs) || utils.DirExists(abs) {
		return abs, nil
	}
	paths, err := container.ResolveOverlayPaths(resolved)
	if err != nil || len(paths) != 1 {
		return "", fmt.Errorf("overlay %s not found", arg)
	}
	return paths[0], nil
}
