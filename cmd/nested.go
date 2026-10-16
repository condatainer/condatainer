package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/settings"
)

// nested_run values.
const (
	nestedRunAuto  = "auto"
	nestedRunTrue  = "true"
	nestedRunFalse = "false"
)

var keyNestedRun = settings.Enum("nested_run",
	settings.Values(nestedRunAuto, nestedRunTrue, nestedRunFalse),
	settings.Default(nestedRunAuto), settings.Order(4),
	settings.Help("Whether a container gets the apptainer installed in libexec for nested runs: auto binds it when installed, true also refuses a launch when it is not installed, false gives none."))

// nestedRun reports whether one e/exec/run launch binds libexec, which holds the apptainer a nested run uses.
func nestedRun() (bool, error) {
	mode := keyNestedRun.Get()
	if mode == nestedRunFalse {
		return false, nil
	}
	if libexec.Installed(libexec.PackageApptainer) {
		return true, nil
	}
	if mode == nestedRunTrue {
		return false, fmt.Errorf("nested_run is true but %w", libexec.NotInstalledError(libexec.PackageApptainer))
	}
	return false, nil
}

// describeNested says how a launch would get apptainer for nested running; "" when it would not.
func describeNested() string {
	if bind, err := nestedRun(); err == nil && bind {
		return "libexec apptainer (bound)"
	}
	return ""
}
