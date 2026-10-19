package toolpath

import "os/exec"

// apptainerOnPath returns the apptainer (or singularity) found on PATH, "" when neither is.
var apptainerOnPath = func() string {
	for _, name := range []string{"apptainer", "singularity"} {
		if binPath, err := exec.LookPath(name); err == nil {
			return binPath
		}
	}
	return ""
}
