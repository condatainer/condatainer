package container

import (
	"path/filepath"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/utils"
)

// ResolveEnvOverlay resolves the project's environment overlay in cwd: the writable overlay (.img or .ext3) if one exists, else the read-only env-typed .sqf.
//   - It never creates anything; callers check utils.IsImg on the result.
//   - It lives here so internal/project can call it without importing internal/helper, which imports it.
func ResolveEnvOverlay(envImg, cwd string) string {
	if p := utils.FindEnvOverlay(envImg, cwd); p != "" {
		return p
	}
	if envImg != "" && envImg != "env.img" {
		return ""
	}
	return FindEnvSnapshot(cwd)
}

// FindEnvSnapshot looks in cwd for the env-typed env.sqf. It returns "" if there
// is none.
func FindEnvSnapshot(cwd string) string {
	wd := utils.ResolveWD(cwd)
	return lookupSlot(filepath.Join(wd, meta.EnvName+".sqf")).Path
}
