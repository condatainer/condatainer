package helper

import (
	"context"
	"fmt"
	"os"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/image"
	"github.com/condatainer/condatainer/internal/logging"
	"github.com/condatainer/condatainer/internal/project"
	"github.com/condatainer/condatainer/internal/runtime/container"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// EnvStatus describes the state of an env overlay path for UI display.
// Returned by CheckEnv. Both CLI and server consume this directly.
//
// SizeMB and Snapshot describe the pair, not the .img alone: a thin .img on
// top of a multi-gigabyte snapshot would otherwise report a misleadingly
// small size, and there would be no way to tell which snapshot it continues
// from at all.
type EnvStatus struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	InUse    bool   `json:"in_use"`
	Writable bool   `json:"writable"`
	SizeMB   int64  `json:"size_mb,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

// ResolveEnv returns the env overlay path that the folder-based convention
// would auto-pick for the given cwd, or "" if none exists.
// Thin wrapper around ResolveEnvOverlayInDir for symmetry with CheckEnv.
func ResolveEnv(cwd string) string {
	return ResolveEnvOverlayInDir("", cwd)
}

// CheckEnv inspects one overlay path: existence, lock status, type, size.
//   - An empty path returns the zero EnvStatus.
//   - The lock probe asks for what a mount would: exclusive for a writable .img, shared for a read-only .sqf.
//   - SizeMB and Snapshot come from container.PairedSize, read even when path does not exist.
//   - That tells "snapshot only" from "nothing here".
func CheckEnv(ctx context.Context, path string) (EnvStatus, error) {
	if path == "" {
		return EnvStatus{}, nil
	}
	st := EnvStatus{Path: path}
	isImg := utils.IsImg(path)

	if _, err := os.Stat(path); err == nil {
		st.Exists = true
		st.Writable = isImg
		if err := image.CheckAvailable(path, st.Writable); err != nil {
			st.InUse = true
		}
	} else if !os.IsNotExist(err) {
		return st, err
	}

	sizeBytes, snapshot := container.PairedSize(path)
	st.SizeMB = sizeBytes / (1024 * 1024)
	st.Snapshot = snapshot
	return st, nil
}

// MissingParams returns the #PARAM: entries from scriptPath that need user
// input — i.e. not present in `supplied`, not optional (KEY=?), and without
// a literal default.
//
// Pure: no prompts, no I/O beyond reading the script file.
func MissingParams(scriptPath string, supplied map[string]string) ([]HelperParam, error) {
	params, err := ParseHelperParams(scriptPath)
	if err != nil {
		return nil, err
	}
	var missing []HelperParam
	for _, p := range params {
		if v, ok := supplied[p.Key]; ok && v != "" {
			continue
		}
		// Optional params (KEY=?) auto-fill from #VALUE: or pass empty.
		if p.Optional {
			continue
		}
		// Literal defaults auto-fill — not missing.
		if p.Default != "" {
			continue
		}
		missing = append(missing, p)
	}
	return missing, nil
}

// Running returns the active HelperRun records for name (empty = any).
// Symmetric alias for RunningHelpers.
func Running(name string) ([]*HelperRun, error) {
	return RunningHelpers(name)
}

// SingletonBlocked reports whether the script's #SINGLETON: meta forbids a
// new launch given the running set.
func SingletonBlocked(meta HelperScriptMeta, running []*HelperRun) bool {
	return meta.Singleton && len(running) > 0
}

// CheckRequiredOverlays expands {tokens} in requiredTemplate from params and returns the resolved absolute paths.
//   - An empty template returns nil.
//   - In a project a name resolves as `exec -o` does. A pinned artifact absent here names `project restore`.
//   - Otherwise, or with noProject, a name nothing installed answers is built with `condatainer create`.
//   - A helper never adds a pin.
func CheckRequiredOverlays(ctx context.Context, cwd, requiredTemplate string, params map[string]string, noProject bool) ([]string, error) {
	if requiredTemplate == "" {
		return nil, nil
	}
	logger := logging.FromContext(ctx)
	names := resolveOverlayTemplate(requiredTemplate, params)
	if noProject {
		logger.Info("Checking required overlays")
		return checkAndInstallNamedOverlays(ctx, names, config.ResolvedDefaultDistro())
	}
	standing, err := project.StandingAt(cwd)
	if err != nil {
		return nil, err
	}
	if standing == nil {
		logger.Info("Checking required overlays")
		return checkAndInstallNamedOverlays(ctx, names, config.ResolvedDefaultDistro())
	}
	logger.Info("Checking required overlays", "project", standing.Root)
	mounts, err := standing.ResolveNames(ctx, names)
	if err != nil {
		return nil, err
	}
	var missing []string
	for i, mount := range mounts {
		if mount.Path == "" {
			missing = append(missing, names[i])
		} else if mount.Live && mount.Name != catalog.Normalize(names[i]) {
			logger.Info(fmt.Sprintf("%s -> %s", names[i], mount.Name), "kind", "note")
		}
	}
	built := map[string]string{}
	if len(missing) > 0 {
		paths, err := checkAndInstallNamedOverlays(ctx, missing, standing.DefaultDistro())
		if err != nil {
			return nil, err
		}
		for i, name := range missing {
			built[name] = paths[i]
		}
	}
	out := make([]string, len(names))
	for i, mount := range mounts {
		out[i] = mount.Path
		if mount.Path == "" {
			out[i] = built[names[i]]
		}
	}
	return out, nil
}

// CheckHelperPackages verifies that every package in meta.ImgPackages is
// installed in the conda environment inside envImg. {KEY} tokens in ImgPackages
// are substituted from params before checking.
// Public wrapper around checkPackages so the server can run pre-submission
// validation without going through PlanRun.
func CheckHelperPackages(meta HelperScriptMeta, envImg string, params map[string]string) error {
	return checkPackages(meta, envImg, params)
}

// ResolveResources merges script headers with config defaults and explicit overrides.
//   - Pass a non-nil overrides to apply user-supplied resource values on top.
//   - Pure (no logger calls).
func ResolveResources(ctx context.Context, scriptPath string, overrides *scheduler.ResourceSpec) *scheduler.ResourceSpec {
	return resolveSpec(scriptPath, overrides)
}
