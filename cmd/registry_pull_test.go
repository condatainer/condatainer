package cmd

import (
	"path/filepath"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
)

func TestRegistryPullDestination(t *testing.T) {
	// The root dir is cached per process, so which tier is writable cannot be
	// pinned from here; the destination is asserted against whichever one is.
	images, err := config.GetWritableImagesDir()
	if err != nil {
		t.Skipf("no writable images directory: %v", err)
	}

	dest, err := registryPullDestination("", "hello/1.0", "")
	if err != nil || dest != filepath.Join(images, "hello--1.0.sqf") {
		t.Fatalf("published-name destination = (%q, %v)", dest, err)
	}
	dest, err = registryPullDestination(filepath.Join(t.TempDir(), "custom"), "hello/1.0", "")
	if err != nil || filepath.Base(dest) != "custom.sqf" {
		t.Fatalf("--prefix destination = (%q, %v)", dest, err)
	}
	dest, err = registryPullDestination(filepath.Join(t.TempDir(), "custom.v1"), "hello/1.0", "")
	if err != nil || filepath.Base(dest) != "custom.v1.sqf" {
		t.Errorf("extensionless dotted prefix = (%q, %v)", dest, err)
	}
	dest, err = registryPullDestination("", "", "hello/1.0")
	if err != nil || dest != filepath.Join(images, "hello--1.0.sqf") {
		t.Fatalf("destination from the published title = (%q, %v)", dest, err)
	}
	if _, err := registryPullDestination("", "", ""); err == nil {
		t.Error("a digest without a title guessed an install name")
	}
}
