package ui

import (
	"fmt"
	"regexp"
	"strings"
)

// envPackageTokenRe matches {KEY} tokens in an #ENV_PACKAGES: template.
var envPackageTokenRe = regexp.MustCompile(`\{([A-Z][A-Z0-9_]*)\}`)

// extractEnvPackageTokens returns the unique {KEY} token names from an #ENV_PACKAGES: template.
func extractEnvPackageTokens(envPackages string) []string {
	matches := envPackageTokenRe.FindAllStringSubmatch(envPackages, -1)
	seen := map[string]bool{}
	var result []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			result = append(result, m[1])
		}
	}
	return result
}

// matchVersion does a partial version match against a list sorted newest-first.
//   - Input "3.12" matches the first entry beginning with "3.12." (latest patch).
//   - Exact match always takes priority.
//   - Returns input unchanged if nothing matches.
func matchVersion(input string, versions []string) string {
	for _, v := range versions {
		if v == input {
			return v
		}
	}
	prefix := input + "."
	for _, v := range versions {
		if strings.HasPrefix(v, prefix) {
			return v
		}
	}
	return input
}

// latestVersion returns the first (newest) entry in a versions list, or "" if empty.
func latestVersion(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}

// versionChoicesDisplay formats a newest-first full-patch list into grouped minor
// version summary strings: ["3.13.2","3.12.10","3.11.12"] →
// ["3.13(.2)", "3.12(.10)", "3.11(.12)"].
func versionChoicesDisplay(versions []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, v := range versions {
		parts := strings.SplitN(v, ".", 3)
		if len(parts) < 2 {
			continue
		}
		minor := parts[0] + "." + parts[1]
		if !seen[minor] {
			seen[minor] = true
			if len(parts) == 3 {
				result = append(result, fmt.Sprintf("%s(.%s)", minor, parts[2]))
			} else {
				result = append(result, minor)
			}
		}
	}
	return result
}
