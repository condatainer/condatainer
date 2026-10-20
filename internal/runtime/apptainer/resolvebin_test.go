package apptainer

import (
	"bytes"
	"context"
	"errors"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withLibexecTier points CNT_LIBEXEC at a fresh directory, so a test decides
// for itself whether the self-provisioned toolchain looks provisioned.
func withLibexecTier(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "libexec")
	t.Setenv("CNT_LIBEXEC", dir)
	return dir
}

// writeFakeBin writes an executable shell script at dir/bin/name that prints
// output verbatim to stdout, standing in for a real apptainer/singularity
// binary so Version's "--version" subprocess has something to read.
func writeFakeBin(t *testing.T, dir, name, output string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// micromamba is what marks the toolchain provisioned.
	if err := os.WriteFile(filepath.Join(bin, "micromamba"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, name)
	script := "#!/bin/sh\necho '" + output + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// resetApptainerState clears the remembered binary between tests and pretends
// to be on a host, so one test's resolution does not leak into the next.
func resetApptainerState(t *testing.T) {
	t.Helper()
	prevLast, prevInside := last, insideContainer
	last, insideContainer = Bin{}, func() bool { return false }
	t.Cleanup(func() { last, insideContainer = prevLast, prevInside })
}

// systemApptainer points the configured system/module binary at path.
func systemApptainer(t *testing.T, path string) {
	t.Helper()
	settingstest.Override(t, "host_apptainer", path)
	if _, err := os.Stat(path); err != nil {
		t.Setenv("PATH", t.TempDir()) // a missing configured binary falls back to PATH, so keep the host's out
	}
}

// With no installed libexec apptainer and no usable system one, Normal refuses
// and names the toolchain install.
func TestNormalRefusesWithNothingUsable(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	systemApptainer(t, filepath.Join(t.TempDir(), "no-apptainer"))

	_, err := Normal()
	if err == nil {
		t.Fatal("Normal succeeded with no apptainer anywhere")
	}
	for _, want := range []string{"update --libexec apptainer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

// Inside a container the same refusal points at nested running, not at a fix
// that is refused there.
func TestNormalInsideContainerPointsAtNestedRun(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	insideContainer = func() bool { return true }
	systemApptainer(t, "")

	_, err := Normal()
	if err == nil || !strings.Contains(err.Error(), "nested_run") || strings.Contains(err.Error(), "update --libexec") || strings.Contains(err.Error(), "not available") {
		t.Fatalf("Normal error = %v, want it to mention nested_run, and neither update --libexec nor \"not available\"", err)
	}
}

// Without libexec's apptainer, a system one that clears the floor is used.
func TestNormalFallsBackToSystem(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	sysBin := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, sysBin)

	bin, err := Normal()
	if err != nil {
		t.Fatalf("Normal: %v", err)
	}
	if bin.Path != sysBin {
		t.Errorf("Normal = %+v, want the system binary %q", bin, sysBin)
	}
}

// A system apptainer below the zstd floor is refused, with the reason.
func TestNormalRefusesOldSystemApptainer(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	systemApptainer(t, writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.3.9"))

	if _, err := Normal(); err == nil || !strings.Contains(err.Error(), "zstd") {
		t.Fatalf("Normal error = %v, want it to name zstd", err)
	}
}

// An apptainer installed in libexec wins over the system binary.
func TestNormalUsesLibexec(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	binPath := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, "/should/not/be/used")

	bin, err := Normal()
	if err != nil {
		t.Fatalf("Normal: %v", err)
	}
	if bin.Path != binPath {
		t.Errorf("Normal = %+v, want the libexec binary %q", bin, binPath)
	}
}

// A fakeroot exec uses the apptainer installed in libexec, else the system one.
func TestFakerootUsesLibexecThenSystem(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	sysBin := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, sysBin)

	bin, err := Fakeroot()
	if err != nil || bin.Path != sysBin {
		t.Fatalf("Fakeroot with no libexec apptainer = %+v, %v; want the system binary %q", bin, err, sysBin)
	}

	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	bin, err = Fakeroot()
	if err != nil || bin.Path != libexecBin {
		t.Errorf("Fakeroot = %+v, %v; want the libexec binary %q", bin, err, libexecBin)
	}
}

// A definition build uses the configured host_apptainer, else libexec's, else the one on PATH.
func TestForBuildPrefersConfiguredThenLibexecThenPath(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	onPath := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.2")
	t.Setenv("PATH", filepath.Dir(onPath))
	if bin, err := ForBuild(); err != nil || bin.Path != onPath {
		t.Fatalf("ForBuild with only PATH = %+v, %v; want %q", bin, err, onPath)
	}

	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	if bin, err := ForBuild(); err != nil || bin.Path != libexecBin {
		t.Fatalf("ForBuild with libexec and PATH = %+v, %v; want the libexec binary %q", bin, err, libexecBin)
	}

	sysBin := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, sysBin)
	if bin, err := ForBuild(); err != nil || bin.Path != sysBin {
		t.Errorf("ForBuild with host_apptainer set = %+v, %v; want %q", bin, err, sysBin)
	}
}

// A system apptainer below the zstd floor cannot mount what condatainer packs,
// and singularity cannot at any version, so a fakeroot exec refuses rather than
// failing later inside Apptainer's own mount step.
func TestFakerootRefusesOldApptainerAndSingularity(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	for _, c := range []struct{ name, output, want string }{
		{"apptainer", "apptainer version 1.3.9", "zstd"},
		{"singularity", "singularity-ce version 4.1.1", "singularity"},
	} {
		systemApptainer(t, writeFakeBin(t, t.TempDir(), c.name, c.output))
		if _, err := Fakeroot(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Fakeroot with %s: error = %v, want it to name %q", c.name, err, c.want)
		}
	}
}

// A definition build has no version check: its output is a sandbox.
func TestForBuildAcceptsAnySystemBinary(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	systemApptainer(t, writeFakeBin(t, t.TempDir(), "singularity", "singularity-ce version 3.0.0"))

	if _, err := ForBuild(); err != nil {
		t.Fatalf("ForBuild: %v", err)
	}
}

// A container has no setuid starter, so both refuse at once.
func TestFakerootAndForBuildRefuseInsideContainer(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	insideContainer = func() bool { return true }
	systemApptainer(t, writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3"))

	for name, resolve := range map[string]func() (Bin, error){"Fakeroot": Fakeroot, "ForBuild": ForBuild} {
		if _, err := resolve(); !errors.Is(err, ErrNeedsHost) {
			t.Errorf("%s error = %v, want ErrNeedsHost", name, err)
		}
	}
}

// A binary's version is read from the per-user cache before the binary is run,
// so a new process does not run it again while it is unchanged.
func TestVersionUsesThePerUserCache(t *testing.T) {
	if insideContainer() {
		t.Skip("the cache is off inside a container")
	}
	counter := filepath.Join(t.TempDir(), "runs")
	path := filepath.Join(t.TempDir(), "apptainer")
	script := "#!/bin/sh\necho x >> '" + counter + "'\necho 'apptainer version 1.5.3'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := Bin{Path: path}

	for i := 0; i < 2; i++ {
		versions.Delete(path) // a new process has no in-memory answer
		if v, err := bin.Version(); err != nil || v != "1.5.3" {
			t.Fatalf("Version = %q, %v", v, err)
		}
	}
	data, _ := os.ReadFile(counter)
	if runs := strings.Count(string(data), "x"); runs != 1 {
		t.Errorf("the binary ran %d times, want 1", runs)
	}
}

func TestRunApptainerDropsUnsetEnv(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	binPath := writeFakeBin(t, dir, "apptainer", "unused")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", "/nonexistent")
	t.Setenv("KEEP_ME", "1")

	bin, err := Normal()
	if err != nil {
		t.Fatalf("Normal: %v", err)
	}

	var out bytes.Buffer
	if err := runApptainerWithOutput(context.Background(), bin, "exec", "", false, nil, &out, &out, nil, []string{"SSL_CERT_FILE"}, 0); err != nil {
		t.Fatalf("runApptainerWithOutput: %v", err)
	}
	if strings.Contains(out.String(), "SSL_CERT_FILE=") {
		t.Errorf("SSL_CERT_FILE reached the launch environment:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "KEEP_ME=1") {
		t.Errorf("unrelated variable was dropped:\n%s", out.String())
	}
}

// Apptainer's INFO lines are hidden with -q, except when debugging.
func TestRunApptainerIsQuietUnlessDebug(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	binPath := writeFakeBin(t, dir, "apptainer", "unused")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := Normal()
	if err != nil {
		t.Fatalf("Normal: %v", err)
	}

	t.Cleanup(func() { utils.DebugMode = false })
	for _, tc := range []struct {
		debug bool
		want  string
	}{
		{false, "-q exec image cmd"},
		{true, "exec image cmd"},
	} {
		utils.DebugMode = tc.debug
		var out bytes.Buffer
		if err := runApptainerWithOutput(context.Background(), bin, "exec", "", false, nil, &out, &out, nil, nil, 0, "exec", "image", "cmd"); err != nil {
			t.Fatalf("runApptainerWithOutput: %v", err)
		}
		if got := strings.TrimSpace(out.String()); got != tc.want {
			t.Errorf("debug=%v: arguments = %q, want %q", tc.debug, got, tc.want)
		}
	}
}
