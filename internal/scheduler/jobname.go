package scheduler

import (
	"fmt"
	"regexp"
	"strings"
)

// JobNameChars describes what a job name may contain.
const JobNameChars = "letters, digits, '.', '_' and '-'"

var (
	jobNameRe    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	jobNameUnfit = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

// ParseJobName returns name when it is a valid job name, else an error naming the allowed characters.
//   - A header's #NAME: and the --name flag share it.
func ParseJobName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !jobNameRe.MatchString(name) {
		return "", fmt.Errorf("%q is not a job name: use %s", name, JobNameChars)
	}
	return name, nil
}

// SanitizeJobName makes a derived name valid by replacing each disallowed character with '-'.
func SanitizeJobName(name string) string {
	return jobNameUnfit.ReplaceAllString(name, "-")
}
