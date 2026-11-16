package cmd

import (
	"context"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/utils"
)

// standingProject reports the project the caller is standing in, or nil when
// there is none or --no-project was given.
func standingProject() (*project.Standing, error) {
	if noProjectRequested {
		return nil, nil
	}
	standing, err := standingProjectHere()
	if err != nil || standing == nil {
		return nil, err
	}
	announceProject(standing.Root)
	return standing, nil
}

// projectOverlays resolves `-o` arguments: through the lock of the project the caller is standing in, else against what is installed.
//   - `exec` and `e` share it.
//   - `--no-project` leaves the lock out, and so does standing outside the project.
func projectOverlays(ctx context.Context, overlays []string) ([]string, error) {
	if len(overlays) == 0 {
		return overlays, nil
	}
	standing, err := standingProject()
	if err != nil {
		return nil, err
	}
	return resolveOverlayValues(ctx, overlays, standing, false)
}

// projectDefaultDistro reports the distro every bare-name alias expands
// against: the standing project's selected root if there is one, else the
// configured default_distro. Every site that expands "<name>" to
// "<distro>/<name>" — completion, display, and expandBareName's build target
// — reads this instead of config.ResolvedDefaultDistro() directly, so
// `project set-distro` changes what all of them mean at once.
func projectDefaultDistro() string {
	if distro := projectSelectedDistro(); distro != "" {
		return distro
	}
	return config.ResolvedDefaultDistro()
}

// completionProject reads the standing project's lock once, for completion: the distro bare names expand under, and the names the project pins.
//   - Outside a project, or when its lock does not load, the distro is the configured default and nothing is pinned.
//   - `--no-project` leaves the pins out. The distro stays the project's, as in projectDefaultDistro.
//   - Only `name/version` keys are pinned names. The root (`base:`), a path pin and a version constraint are not, and neither is a key with no version.
func completionProject() (distro string, pinned []string) {
	standing, err := standingProjectHere()
	if err != nil || standing == nil {
		return config.ResolvedDefaultDistro(), nil
	}
	distro = standing.DefaultDistro()
	if noProjectRequested {
		return distro, nil
	}
	for key := range standing.Lock.Pins {
		if key == lock.BaseKey || strings.HasPrefix(key, lock.PathPrefix) {
			continue
		}
		if dep, err := utils.ParseDep(key); err == nil && dep.Op == "" && dep.Version != "" {
			pinned = append(pinned, key)
		}
	}
	return distro, pinned
}

// projectSelectedDistro reports the distro named by the standing project's
// base pin, or "" when there is no project, no base pin, or its lookup fails
// for any reason — the safe default for a completion or display path, which
// falls back to the configured default_distro rather than erroring.
func projectSelectedDistro() string {
	standing, err := standingProjectHere()
	if err != nil || standing == nil {
		return ""
	}
	return standing.SelectedDistro()
}

// projectBaseImage resolves the project's locked root to a local path, for a caller standing in a project that did not request a root.
//   - It returns "" and no error otherwise, so ensureRootBaseImage falls through to the configured default.
//   - It is strict, unlike projectOverlays: an unresolved pin refuses naming `project restore`, never falling back to the machine's default_distro.
func projectBaseImage(ctx context.Context) (string, error) {
	if noProjectRequested {
		return "", nil
	}
	standing, err := standingProjectHere()
	if err != nil || standing == nil {
		return "", err
	}
	announceProject(standing.Root)
	return standing.Base(ctx)
}

// splitOverlayMode separates a `:ro`/`:rw` mount mode from what it applies to.
func splitOverlayMode(overlay string) (value, suffix string) {
	for _, mode := range []string{":ro", ":rw"} {
		if trimmed, ok := strings.CutSuffix(overlay, mode); ok {
			return trimmed, mode
		}
	}
	return overlay, ""
}
