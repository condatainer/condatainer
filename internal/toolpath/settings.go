package toolpath

import (
	"fmt"
	"os/exec"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
)

const keyApptainerName = "host_apptainer"

var keyHostApptainer = settings.String(keyApptainerName,
	settings.AllowEmpty(), settings.Order(2),
	settings.Check(func(stored string) error {
		if !utils.ValidateBinary(stored) {
			return fmt.Errorf("%q is not an executable file or a command on PATH", stored)
		}
		return nil
	}),
	settings.Help("The apptainer of the host. When set, it runs containers and builds .def files outside a container, in place of the toolchain's. Unset, no host apptainer is used."))

func init() {
	keyHostApptainer.SetShow(func(string) string { return HostApptainer() })
}

// configuredApptainer is host_apptainer when a layer, the environment or a flag sets it.
func configuredApptainer() string {
	if res, _ := settings.Resolve(keyApptainerName); res.Source != settings.SourceDefault {
		return res.Value
	}
	return ""
}

// HostApptainer is the host_apptainer that a layer, the environment or a flag sets, when it can be run. "" otherwise.
// A bare command name is returned as the path it names on PATH now, so which apptainer runs is fixed at this call.
func HostApptainer() string {
	bin := configuredApptainer()
	if bin == "" || !utils.ValidateBinary(bin) {
		return ""
	}
	if path, err := exec.LookPath(bin); err == nil {
		return path
	}
	return bin
}

// InvalidHostApptainer is the configured host_apptainer when it is set but cannot be run, "" otherwise.
func InvalidHostApptainer() string {
	if bin := configuredApptainer(); bin != "" && !utils.ValidateBinary(bin) {
		return bin
	}
	return ""
}
