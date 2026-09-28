package libexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"apptainer version 1.5.3":        "1.5.3",
		"1.4.0":                          "1.4.0",
		"singularity-ce version 4.1.1\n": "4.1.1",
		"":                               "",
		"no version here":                "",
	}
	for input, want := range cases {
		if got := parseVersion(input); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMeetsFloor(t *testing.T) {
	cases := map[string]bool{
		"1.5.3": true,
		"1.4.0": true,
		"1.3.9": false,
		"1.0.0": false,
		"2.0.0": true,
		"0.9.0": false,
		"1":     false, // no minor component to compare
	}
	for version, want := range cases {
		if got := meetsFloor(version, 1, 4); got != want {
			t.Errorf("meetsFloor(%q, 1, 4) = %v, want %v", version, got, want)
		}
	}
}

// mksquashfs and unsquashfs only recognize their own single-dash "-version",
// not the GNU-style "--version" every other provisioned tool takes.
func TestVersionFlag(t *testing.T) {
	cases := map[string]string{
		"mksquashfs": "-version",
		"unsquashfs": "-version",
		"apptainer":  "--version",
		"squashfuse": "--version",
		"micromamba": "--version",
	}
	for name, want := range cases {
		if got := versionFlag(name); got != want {
			t.Errorf("versionFlag(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMicromambaAssetName(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the self-provisioned toolchain is linux-only")
	}
	asset, err := micromambaAssetName()
	switch runtime.GOARCH {
	case "amd64":
		if err != nil || asset != "micromamba-linux-64" {
			t.Errorf("micromambaAssetName() = %q, %v; want micromamba-linux-64, nil", asset, err)
		}
	case "arm64":
		if err != nil || asset != "micromamba-linux-aarch64" {
			t.Errorf("micromambaAssetName() = %q, %v; want micromamba-linux-aarch64, nil", asset, err)
		}
	default:
		if err == nil {
			t.Errorf("micromambaAssetName() = %q, nil; want an error for unsupported arch %s", asset, runtime.GOARCH)
		}
	}
}

// Each refusal comes before any download, and leaves the directory as it was.
func TestUpdateRefusals(t *testing.T) {
	t.Run("another update holds the lock", func(t *testing.T) {
		dir := withLibexecDir(t)
		provisionedStub(t, dir)
		other, err := acquireUpdateLock(filepath.Join(dir, updateLockName))
		if err != nil {
			t.Fatalf("failed to simulate another update: %v", err)
		}
		defer other.Close()

		err = Update(context.Background())
		if err == nil || !strings.Contains(err.Error(), "another condatainer update") {
			t.Fatalf("Update() = %v, want another-update refusal", err)
		}
		if _, ok := Dir(); !ok {
			t.Error("Update() left no provisioned toolchain after refusing")
		}
	})

	t.Run("the directory is not writable", func(t *testing.T) {
		dir := withLibexecDir(t)
		provisionedStub(t, dir)
		if err := os.Chmod(dir, 0555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0755) //nolint:errcheck
		if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
			f.Close()
			t.Skip("running with privileges that ignore permissions")
		}

		err := Update(context.Background())
		if err == nil || !strings.Contains(err.Error(), "not writable") {
			t.Fatalf("Update() = %v, want a not-writable refusal", err)
		}
	})

	t.Run("the directory holds something else", func(t *testing.T) {
		dir := withLibexecDir(t)
		keep := filepath.Join(dir, "data.txt")
		if err := utils.MkdirAllShared(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keep, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}

		err := Update(context.Background())
		if err == nil || !strings.Contains(err.Error(), "is not a toolchain") {
			t.Fatalf("Update() = %v, want a not-a-toolchain refusal", err)
		}
		if !utils.FileExists(keep) {
			t.Error("Update() removed a file from a directory that is not a toolchain")
		}
	})
}

func TestUpdateRejectsAnUnknownPackage(t *testing.T) {
	withLibexecDir(t)
	err := Update(context.Background(), "e2fsprogs")
	if err == nil || !strings.Contains(err.Error(), "unknown toolchain package") {
		t.Errorf("Update(e2fsprogs) error = %v, want an unknown-package error", err)
	}
}

func TestVerifyToolchainChecksOnlyWhatIsInstalled(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := utils.MkdirAllShared(bin); err != nil {
		t.Fatal(err)
	}
	writeFakeVersionBin(t, bin, "micromamba", "2.0.5\n")
	if err := verifyToolchain(context.Background(), dir); err != nil {
		t.Errorf("verifyToolchain(micromamba only) = %v, want nil", err)
	}

	writeFakeVersionBin(t, bin, "apptainer", "apptainer version 1.3.0\n")
	if err := verifyToolchain(context.Background(), dir); err == nil {
		t.Error("verifyToolchain accepted an apptainer below the floor")
	}
}

func TestVersionsRefusesWithNothingProvisioned(t *testing.T) {
	withLibexecDir(t)
	if _, err := Versions(context.Background()); err != ErrNotProvisioned {
		t.Errorf("Versions() error = %v, want ErrNotProvisioned", err)
	}
}

func TestVersionsParsesEachBinary(t *testing.T) {
	dir := withLibexecDir(t)
	provisionedStub(t, dir)
	bin := filepath.Join(dir, "bin")

	// micromamba already exists (provisionedStub's marker); give every
	// tool a version-shaped banner, one printing nothing --version-shaped to
	// prove that case reports "unknown" rather than failing the rest.
	writeFakeVersionBin(t, bin, "apptainer", "apptainer version 1.5.2\n")
	writeFakeVersionBin(t, bin, "mksquashfs", "mksquashfs version 4.6.1 (2023-08-19)\n")
	writeFakeVersionBin(t, bin, "squashfuse", "squashfuse\n") // no version in output
	writeFakeVersionBin(t, bin, "micromamba", "2.0.5\n")

	versions, err := Versions(context.Background())
	if err != nil {
		t.Fatalf("Versions(): %v", err)
	}
	want := map[string]string{
		"apptainer":  "1.5.2",
		"mksquashfs": "4.6.1",
		"squashfuse": "unknown",
		"micromamba": "2.0.5",
	}
	got := map[string]string{}
	for _, v := range versions {
		got[v.Name] = v.Version
	}
	for name, wantVersion := range want {
		if got[name] != wantVersion {
			t.Errorf("%s version = %q, want %q", name, got[name], wantVersion)
		}
	}
}

func TestEnsureFusermount3InBinLinksFromSbin(t *testing.T) {
	prefix := t.TempDir()
	sbin := filepath.Join(prefix, "sbin")
	if err := utils.MkdirAllShared(sbin); err != nil {
		t.Fatalf("failed to create sbin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sbin, "fusermount3"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write stub fusermount3: %v", err)
	}
	if err := utils.MkdirAllShared(filepath.Join(prefix, "bin")); err != nil {
		t.Fatalf("failed to create bin: %v", err)
	}

	if err := ensureFusermount3InBin(prefix); err != nil {
		t.Fatalf("ensureFusermount3InBin(): %v", err)
	}

	link, err := os.Readlink(filepath.Join(prefix, "bin", "fusermount3"))
	if err != nil {
		t.Fatalf("bin/fusermount3 not created as a symlink: %v", err)
	}
	if want := filepath.Join("..", "sbin", "fusermount3"); link != want {
		t.Errorf("bin/fusermount3 -> %q, want %q", link, want)
	}
}

func TestEnsureFusermount3InBinNoOpWhenAlreadyInBin(t *testing.T) {
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "bin")
	if err := utils.MkdirAllShared(bin); err != nil {
		t.Fatalf("failed to create bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "fusermount3"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write stub fusermount3: %v", err)
	}

	if err := ensureFusermount3InBin(prefix); err != nil {
		t.Fatalf("ensureFusermount3InBin(): %v", err)
	}
	info, err := os.Lstat(filepath.Join(bin, "fusermount3"))
	if err != nil {
		t.Fatalf("bin/fusermount3 disappeared: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("ensureFusermount3InBin() replaced an existing bin/fusermount3 with a symlink")
	}
}

func TestEnsureFusermount3InBinNoOpWhenNotInSbinEither(t *testing.T) {
	prefix := t.TempDir()
	if err := utils.MkdirAllShared(filepath.Join(prefix, "bin")); err != nil {
		t.Fatalf("failed to create bin: %v", err)
	}
	if err := ensureFusermount3InBin(prefix); err != nil {
		t.Fatalf("ensureFusermount3InBin(): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(prefix, "bin", "fusermount3")); !os.IsNotExist(err) {
		t.Error("ensureFusermount3InBin() created bin/fusermount3 out of nothing")
	}
}

func writeFakeVersionBin(t *testing.T, bin, name, output string) {
	t.Helper()
	script := "#!/bin/sh\ncat <<'EOF'\n" + output + "EOF\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil {
		t.Fatalf("failed to write stub %s: %v", name, err)
	}
}

// unsquashfs prints its version and exits 1; the floor check reads the output.
func TestCheckFloorIgnoresExitStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unsquashfs")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'unsquashfs version 4.6.1 (2023/03/25)'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckFloor(context.Background(), path, "unsquashfs"); err != nil {
		t.Errorf("CheckFloor = %v, want nil for 4.6.1", err)
	}
	if err := CheckFloor(context.Background(), path, "debugfs"); err != nil {
		t.Errorf("CheckFloor for a name with no floor = %v, want nil", err)
	}
}

func TestSyncRejectsAnUnknownPackage(t *testing.T) {
	withLibexecDir(t)
	err := Sync(context.Background(), "e2fsprogs")
	if err == nil || !strings.Contains(err.Error(), "unknown toolchain package") {
		t.Errorf("Sync(e2fsprogs) error = %v, want an unknown-package error", err)
	}
}
