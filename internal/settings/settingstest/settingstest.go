// Package settingstest sets registered keys from a test.
package settingstest

import (
	"testing"

	"github.com/condatainer/condatainer/internal/settings"
)

// Override sets key to text until the test ends. Tests that use it must not run in parallel.
func Override(t testing.TB, key, text string) {
	t.Helper()
	restore, err := settings.OverrideValue(key, text)
	if err != nil {
		t.Fatalf("override %s: %v", key, err)
	}
	t.Cleanup(restore)
}

// OverrideList sets a list key to elements until the test ends.
func OverrideList(t testing.TB, key string, elements ...string) {
	t.Helper()
	restore, err := settings.OverrideList(key, elements)
	if err != nil {
		t.Fatalf("override %s: %v", key, err)
	}
	t.Cleanup(restore)
}

// ClearFlags drops every flag value the test set by parsing arguments, when it ends.
func ClearFlags(t testing.TB) {
	t.Helper()
	t.Cleanup(settings.ClearFlags)
}

// Declare runs fn, which registers keys of the test's own, and removes them again when the test ends.
func Declare(t testing.TB, fn func()) {
	t.Helper()
	before := map[string]bool{}
	for _, n := range settings.Names() {
		before[n] = true
	}
	fn()
	t.Cleanup(func() {
		var added []string
		for _, n := range settings.Names() {
			if !before[n] {
				added = append(added, n)
			}
		}
		settings.Unregister(added...)
	})
}
