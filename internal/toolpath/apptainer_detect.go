package toolpath

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// apptainerOnPath returns the apptainer (or singularity) found on PATH, "" when neither is.
var apptainerOnPath = func() string {
	for _, name := range []string{"apptainer", "singularity"} {
		if binPath, err := exec.LookPath(name); err == nil {
			return binPath
		}
	}
	return ""
}

// FindApptainerBin returns the apptainer (or singularity) on PATH, else the
// newest one a module provides. "" when there is none.
func FindApptainerBin() string {
	if path := apptainerOnPath(); path != "" {
		return path
	}
	return detectApptainerFromModules()
}

func detectApptainerFromModules() string {
	slog.Default().Info("Searching modules for apptainer/singularity via 'module avail'")

	bestModule := ""
	bestVersion := ""

	for _, moduleName := range []string{"apptainer", "singularity"} {
		module, version := detectLatestModule(moduleName)
		if module == "" {
			continue
		}

		if bestModule == "" || compareModuleVersion(version, bestVersion) > 0 {
			bestModule = module
			bestVersion = version
		}
	}

	if bestModule == "" {
		slog.Default().Debug("no apptainer/singularity modules found via module avail")
		return ""
	}

	slog.Default().Info("Using module candidate", "module", bestModule)

	// Resolve the actual binary path after loading the selected module.
	// Use both names as fallback because module name and binary name can differ.
	cmdStr := fmt.Sprintf("module load %q >/dev/null 2>&1 && (command -v apptainer || command -v singularity)", bestModule)
	out, err := exec.Command("bash", "-lc", cmdStr).Output()
	if err != nil {
		return ""
	}

	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return ""
	}

	line := strings.Split(resolved, "\n")[0]
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	if filepath.IsAbs(line) {
		if info, statErr := os.Stat(line); statErr == nil && info.Mode()&0111 != 0 {
			return line
		}
	}

	if fullPath, lookErr := exec.LookPath(line); lookErr == nil {
		return fullPath
	}

	return ""
}

func detectLatestModule(moduleName string) (module string, version string) {
	cmdStr := fmt.Sprintf("module -t avail %s 2>&1 || true", moduleName)
	out, err := exec.Command("bash", "-lc", cmdStr).Output()
	if err != nil {
		return "", ""
	}

	module, version = parseLatestModuleFromAvailOutput(moduleName, string(out))
	return module, version
}

func parseLatestModuleFromAvailOutput(moduleName, output string) (module string, version string) {
	lineRe := regexp.MustCompile(`^` + regexp.QuoteMeta(moduleName) + `(?:/([^\s()]+))?(?:\([^)]*\))?$`)

	bestModule := ""
	bestVersion := ""

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if strings.Contains(line, ":") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "Lmod") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		for _, token := range fields {
			token = strings.TrimSpace(token)
			token = strings.TrimSuffix(token, "*")

			match := lineRe.FindStringSubmatch(token)
			if len(match) == 0 {
				continue
			}

			moduleVersion := ""
			if len(match) > 1 {
				moduleVersion = match[1]
			}

			cleanModule := moduleName
			if moduleVersion != "" {
				cleanModule = moduleName + "/" + moduleVersion
			}

			if bestModule == "" || compareModuleVersion(moduleVersion, bestVersion) > 0 {
				bestModule = cleanModule
				bestVersion = moduleVersion
			}
		}
	}

	return bestModule, bestVersion
}

func compareModuleVersion(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}

	partsA := splitVersionParts(a)
	partsB := splitVersionParts(b)
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		va := ""
		vb := ""
		if i < len(partsA) {
			va = partsA[i]
		}
		if i < len(partsB) {
			vb = partsB[i]
		}

		na, errA := strconv.Atoi(va)
		nb, errB := strconv.Atoi(vb)

		switch {
		case errA == nil && errB == nil:
			if na > nb {
				return 1
			}
			if na < nb {
				return -1
			}
		case errA == nil && errB != nil:
			return 1
		case errA != nil && errB == nil:
			return -1
		default:
			if va > vb {
				return 1
			}
			if va < vb {
				return -1
			}
		}
	}

	return 0
}

func splitVersionParts(v string) []string {
	parts := strings.FieldsFunc(v, func(r rune) bool {
		switch r {
		case '.', '-', '_', '+':
			return true
		default:
			return false
		}
	})

	if len(parts) == 0 {
		return []string{v}
	}

	return parts
}
