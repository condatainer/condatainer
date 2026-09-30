package cmd

import "testing"

func TestOverlayInitPackagesBlank(t *testing.T) {
	pkgs, err := overlayInitPackages("", nil)
	if err != nil || len(pkgs) != 0 {
		t.Fatalf("blank overlay: got %v, %v", pkgs, err)
	}
}

func TestOverlayInitPackagesRejectsMissingFile(t *testing.T) {
	if _, err := overlayInitPackages("/path/that/does/not/exist.yml", nil); err == nil {
		t.Fatal("expected an error for a missing environment file")
	}
}
