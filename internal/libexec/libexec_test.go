package libexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
)

// withLibexecDir points CNT_LIBEXEC at a fresh, not yet existing directory and
// returns it.
func withLibexecDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "libexec")
	t.Setenv("CNT_LIBEXEC", dir)
	return dir
}

// provisionedStub drops a fake bin/micromamba under dir, just enough to
// satisfy Dir's marker check without a real bootstrap.
func provisionedStub(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := utils.MkdirAllShared(bin); err != nil {
		t.Fatalf("failed to create stub bin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "micromamba"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write stub micromamba: %v", err)
	}
}

func TestDirFindsAProvisionedToolchain(t *testing.T) {
	dir := withLibexecDir(t)
	provisionedStub(t, dir)

	got, ok := Dir()
	if !ok {
		t.Fatal("Dir() = not found, want the provisioned directory")
	}
	if want := realPath(t, dir); got != want {
		t.Errorf("Dir() = %s, want %s", got, want)
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestDirIgnoresAnUnprovisionedDir(t *testing.T) {
	dir := withLibexecDir(t)
	if _, ok := Dir(); ok {
		t.Fatal("Dir() found a toolchain in a directory that does not exist")
	}
	// libexec/ exists but was never provisioned (no bin/micromamba), as a
	// crashed create leaves it.
	if err := utils.MkdirAllShared(dir); err != nil {
		t.Fatalf("failed to create empty libexec dir: %v", err)
	}
	if got, ok := Dir(); ok {
		t.Fatalf("Dir() = %s, want not found (empty libexec dir)", got)
	}
}

// The baked-in prefix path and the container bind are both the real path.
func TestDirResolvesASymlinkedLocation(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real-libexec")
	provisionedStub(t, real)
	link := filepath.Join(t.TempDir(), "libexec")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CNT_LIBEXEC", link)

	if got, ok := Dir(); !ok || got != realPath(t, real) {
		t.Errorf("Dir() = %s, %v; want %s, true", got, ok, realPath(t, real))
	}
}

func TestPathReportsOnlyInstalledTools(t *testing.T) {
	dir := withLibexecDir(t)
	provisionedStub(t, dir)
	bin := filepath.Join(dir, "bin")

	if _, ok := ApptainerPath(); ok {
		t.Error("ApptainerPath() found an apptainer that is not installed")
	}
	if Installed("apptainer") {
		t.Error("Installed(apptainer) = true, want false")
	}

	if err := os.WriteFile(filepath.Join(bin, "apptainer"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	path, ok := ApptainerPath()
	if !ok {
		t.Fatal("ApptainerPath() = not found after installing it")
	}
	if want := filepath.Join(bin, "apptainer"); path != want {
		t.Errorf("ApptainerPath() = %s, want %s", path, want)
	}
	if _, ok := Path("mksquashfs"); ok {
		t.Error("Path(mksquashfs) found a mksquashfs that is not installed")
	}
}

func TestMessagesNameThePackage(t *testing.T) {
	if got := NotProvisionedMessage("mksquashfs"); !strings.Contains(got, "update --libexec squashfs-tools") {
		t.Errorf("NotProvisionedMessage(mksquashfs) = %q, want it to name squashfs-tools", got)
	}
	if got := NotProvisionedMessage("debugfs"); strings.Contains(got, "update --libexec") {
		t.Errorf("NotProvisionedMessage(debugfs) = %q, must not suggest an install", got)
	}
}

func TestEnsureMicromambaLeavesAProvisionedToolchainAlone(t *testing.T) {
	dir := withLibexecDir(t)
	provisionedStub(t, dir)

	if err := EnsureMicromamba(context.Background()); err != nil {
		t.Fatalf("EnsureMicromamba on a provisioned toolchain: %v", err)
	}
}
