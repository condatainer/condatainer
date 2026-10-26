package build

import (
	"fmt"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// GetTypeFromScript returns the payload type an external build script declares with #TYPE:.
//   - Only "app" and "data" are accepted, as in DeriveType. #TYPE: means the same to a recipe and to a script.
//   - A script that declares none is an app.
func GetTypeFromScript(scriptPath string) (string, error) {
	if !utils.FileExists(scriptPath) {
		return "", fmt.Errorf("build script not found at %s", scriptPath)
	}
	text, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", fmt.Errorf("failed to open script: %w", err)
	}

	for _, a := range utils.SelectAnnotations(utils.ScanAnnotations(text), "#TYPE") {
		value := strings.ToLower(a.Value)
		if value == "" {
			continue
		}
		switch value {
		case "app", "data":
			return value, nil
		default:
			return "", fmt.Errorf("invalid TYPE value %q: valid values are app or data", value)
		}
	}
	return "app", nil
}

// GetTargetFromScript returns the artifact name an external build script declares with #TARGET:, or "" when it declares none.
//   - The name sets the payload's /cnt/<name> prefix, and key.Role reads it to decide which dependencies count toward equivalence.
//   - It comes from the script, not the `-p` path, so the same script built to two paths classifies its dependencies one way.
//   - A {placeholder} is refused, since an external build has no #PH: values to fill it.
func GetTargetFromScript(scriptPath string) (string, error) {
	if !utils.FileExists(scriptPath) {
		return "", fmt.Errorf("build script not found at %s", scriptPath)
	}
	text, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", fmt.Errorf("failed to open script: %w", err)
	}

	for _, a := range utils.SelectAnnotations(utils.ScanAnnotations(text), "#TARGET") {
		value := strings.TrimSpace(a.Value)
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, "{}") {
			return "", fmt.Errorf("invalid TARGET value %q: an external build takes a plain name, not a {placeholder}", value)
		}
		// Trimmed and checked for empty components, because Normalize does
		// neither and key.Role splits the name on "/". A stray slash would leave an
		// empty component, which quietly stops a dependency matching and downgrades
		// it to build history — the exact misclassification #TARGET: exists to stop.
		name := strings.Trim(utils.NormalizeModule(value), "/")
		if name == "" {
			continue
		}
		for _, component := range strings.Split(name, "/") {
			if component == "" {
				return "", fmt.Errorf("invalid TARGET value %q: it has an empty path component", value)
			}
		}
		return name, nil
	}
	return "", nil
}
