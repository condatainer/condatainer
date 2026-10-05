package scheduler

import (
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// envItems splits the value of an environment directive into its
// comma-separated items, without surrounding quotes or spaces.
func envItems(value string) []string {
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// fullEnvValue turns the value of an environment directive into one that
// copies the whole submit environment: "all", then the NAME=value assignments
// the directive carried. limited reports whether the directive asked for less.
func fullEnvValue(value, all, sep string) (full string, limited bool) {
	limited = true
	parts := []string{all}
	for _, item := range envItems(value) {
		switch {
		case strings.EqualFold(item, all):
			limited = false
		case strings.Contains(item, "="):
			parts = append(parts, item)
		}
	}
	return strings.Join(parts, sep), limited
}

// takeFlag removes the directives matching any prefix from flags and returns
// the value of the last one. found is false when none matched.
func takeFlag(flags []string, prefixes ...string) (rest []string, value string, found bool) {
	for _, flag := range flags {
		if v, ok := flagValue(flag, prefixes...); ok {
			value, found = v, true
			continue
		}
		rest = append(rest, flag)
	}
	return rest, value, found
}

// noteEnvOverride tells the user that a limit on the job's environment was
// replaced, except in a job. theirs names who set what, such as "Your script
// sets --export=NONE". Tests replace it.
var noteEnvOverride = func(theirs, ours string) {
	if !IsInsideJob() {
		utils.PrintNote("%s. The job is submitted with %s, so it gets this shell's environment.", theirs, ours)
	}
}
