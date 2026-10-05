package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// ApplyHomeOverride replaces HOME with home_override, once, after the config layers load.
//   - An unset $VAR in the value, or a directory that cannot be created, warns and leaves HOME alone.
//   - Job scripts replace HOME the same way (scheduler.JobHome).
//   - The data search paths and build.logs_dir follow the new HOME; build.logs_dir unless it is configured.
func ApplyHomeOverride() {
	if Global.HomeOverride == "" {
		return
	}
	dir, err := resolveHomeOverride(Global.HomeOverride)
	if err == nil {
		err = utils.MkdirAllShared(dir)
	}
	if err != nil {
		utils.PrintWarning("home_override ignored: %v", err)
		return
	}
	utils.ReplaceHome(dir)
	scheduler.JobHome = dir
	GlobalDataPaths = DataPaths{} // search paths are rebuilt against the new HOME
	if _, set := layerStringSet("build.logs_dir"); !set {
		Global.Build.LogsDir = DefaultLogsDir()
	}
	viper.SetDefault("build.logs_dir", DefaultLogsDir())
}

// resolveHomeOverride expands the environment in raw and makes it absolute.
// A variable that is unset or empty is an error, so $SCRATCH/x never becomes /x.
func resolveHomeOverride(raw string) (string, error) {
	var unset []string
	dir := os.Expand(raw, func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			unset = append(unset, "$"+name)
		}
		return v
	})
	if len(unset) > 0 {
		return "", fmt.Errorf("%s is not set", strings.Join(unset, ", "))
	}
	return filepath.Abs(dir)
}
