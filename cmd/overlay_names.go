package cmd

import (
	"context"
	"fmt"
	"path/filepath"

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
//   - A version constraint is refused.
//   - An unanswered name or a file is returned unchanged outside a project, and is an error naming `project restore` inside.
func resolveOverlayValues(ctx context.Context, values []string, standing *project.Standing) ([]string, error) {
	out, mounts, err := resolveOverlayMounts(ctx, values, standing)
	if err != nil {
		return nil, err
	}
	printMounts(mounts, standing != nil)
	return out, nil
}

// resolveOverlayMounts is resolveOverlayValues with nothing printed. It also returns the mounts the project resolver answered.
func resolveOverlayMounts(ctx context.Context, values []string, standing *project.Standing) ([]string, []project.Mount, error) {
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
			return nil, nil, fmt.Errorf("%s: %s", name, reason)
		}
		requests = append(requests, request)
		suffixes = append(suffixes, suffix)
		kept = append(kept, i)
	}
	if len(requests) == 0 {
		return out, nil, nil
	}

	opts := project.ResolveOptions{LiveResolve: true, Distro: projectDefaultDistro(), Uncached: true}
	var mounts []project.Mount
	if standing != nil {
		resolution, err := standing.ResolveComplete(ctx, requests, opts)
		if err != nil {
			return nil, nil, err
		}
		mounts = resolution.Mounts
	} else {
		var err error
		if mounts, err = project.ResolveUnlocked(ctx, requests, opts); err != nil {
			return nil, nil, err
		}
	}
	if len(mounts) != len(requests) {
		return nil, nil, fmt.Errorf("resolved %d of %d overlays", len(mounts), len(requests))
	}

	for n, mount := range mounts {
		// Outside a project a file or an unanswered name is the resolver's to report.
		if standing == nil && (mount.Unpinned || mount.Path == "") {
			continue
		}
		out[kept[n]] = mount.Path + suffixes[n]
	}
	return out, mounts, nil
}

// depState says where one #DEP: of a script stands.
type depState int

const (
	depInstalled   depState = iota
	depMissingName          // a name nothing installed answers
	depMissingFile          // an overlay file that does not exist
)

// scriptDep is one #DEP: of a script and what a run mounts for it.
type scriptDep struct {
	Request string
	// Path is the overlay a run mounts. It is the request itself while the dependency is missing.
	Path  string
	State depState
}

// Missing reports whether a run stops at the dependency.
func (d scriptDep) Missing() bool { return d.State != depInstalled }

// IsFile reports whether the dependency is an overlay file, not a name.
func (d scriptDep) IsFile() bool { return utils.IsOverlay(d.Request) || utils.IsSif(d.Request) }

// scriptDeps decides what a run mounts for each #DEP: of a script outside a project, with nothing printed.
//   - The mounts are what the resolver answered, for a caller that names them.
//   - A file is installed when it exists, relative to the current directory.
//   - A name the resolver answers is that overlay. A bare or partial name resolves to the newest installed one.
//   - A name it leaves unchanged is installed when the installed map holds its exact name.
//   - A version constraint is refused.
func scriptDeps(ctx context.Context, deps []string) ([]scriptDep, []project.Mount, error) {
	installed, err := getInstalledOverlaysMap()
	if err != nil {
		return nil, nil, err
	}
	solved, mounts, err := resolveOverlayMounts(ctx, deps, nil)
	if err != nil {
		return nil, nil, err
	}
	out := make([]scriptDep, len(deps))
	for i, dep := range deps {
		d := scriptDep{Request: dep, Path: dep}
		switch {
		case d.IsFile():
			if !utils.FileExists(dep) {
				d.State = depMissingFile
			}
		case solved[i] != dep:
			d.Path = solved[i]
		default:
			if path, ok := installed[utils.NormalizeModule(dep)]; ok {
				d.Path = path
			} else {
				d.State = depMissingName
			}
		}
		out[i] = d
	}
	return out, mounts, nil
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
