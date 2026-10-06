package toolpath

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
)

const keyApptainerName = "host_apptainer"

var keyHostApptainer = settings.String(keyApptainerName,
	settings.Default("apptainer"), settings.AllowEmpty(), settings.Order(2),
	settings.Detect(FindApptainerBin),
	settings.Check(func(stored string) error {
		if !utils.ValidateBinary(stored) {
			return fmt.Errorf("%q is not an executable file or a command on PATH", stored)
		}
		return nil
	}),
	settings.Help("The apptainer or singularity of the host or a module. Fakeroot and .def builds need it, and it is used when libexec has none."))

func init() {
	keyHostApptainer.SetShow(func(string) string { return SystemApptainer() })
}

// configuredApptainer is host_apptainer when a layer, the environment or a flag sets it.
func configuredApptainer() string {
	if res, _ := settings.Resolve(keyApptainerName); res.Source != settings.SourceDefault {
		return res.Value
	}
	return ""
}

// SystemApptainer is the apptainer of the system or a module: the configured one when it can be run, else the first on PATH. "" when there is none.
func SystemApptainer() string {
	if bin := configuredApptainer(); bin != "" && utils.ValidateBinary(bin) {
		return bin
	}
	return apptainerOnPath()
}

// InvalidSystemApptainer is the configured host_apptainer when it is set but cannot be run, "" otherwise.
func InvalidSystemApptainer() string {
	if bin := configuredApptainer(); bin != "" && !utils.ValidateBinary(bin) {
		return bin
	}
	return ""
}
