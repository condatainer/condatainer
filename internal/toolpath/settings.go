package toolpath

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
)

const keyApptainerName = "build.system_apptainer"

var keySystemApptainer = settings.String(keyApptainerName,
	settings.Default("apptainer"), settings.AllowEmpty(), settings.Order(1),
	settings.Detect(FindApptainerBin),
	settings.Check(func(stored string) error {
		if !utils.ValidateBinary(stored) {
			return fmt.Errorf("%q is not an executable file or a command on PATH", stored)
		}
		return nil
	}),
	settings.Help("The apptainer or singularity of the system or a module, used when libexec has none."))

func init() {
	keySystemApptainer.SetShow(func(string) string { return SystemApptainer() })
}

// configuredApptainer is build.system_apptainer when a layer, the environment or a flag sets it.
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

// InvalidSystemApptainer is the configured build.system_apptainer when it is set but cannot be run, "" otherwise.
func InvalidSystemApptainer() string {
	if bin := configuredApptainer(); bin != "" && !utils.ValidateBinary(bin) {
		return bin
	}
	return ""
}
