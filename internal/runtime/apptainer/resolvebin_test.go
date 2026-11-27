package apptainer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
)

// The needs of the three kinds of launch.
var (
	overlays = Need{Overlays: true}
	fakeroot = Need{Fakeroot: true, Overlays: true}
	build    = Need{Fakeroot: true}
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
// output verbatim to stdout, standing in for a real apptainer
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
	prevInside := insideContainer
	insideContainer = func() bool { return false }
	t.Cleanup(func() { insideContainer = prevInside })
}

// systemApptainer points the configured system/module binary at path.
func systemApptainer(t *testing.T, path string) {
	t.Helper()
	settingstest.Override(t, "host_apptainer", path)
	if _, err := os.Stat(path); err != nil {
		t.Setenv("PATH", t.TempDir()) // keep the host's PATH out of a bare-name lookup
	}
}

// With no installed libexec apptainer and no host_apptainer, Resolve refuses
// and names the toolchain install and the setting.
func TestResolveRefusesWithNothingUsable(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)

	_, err := Resolve(overlays)
	if err == nil {
		t.Fatal("Resolve succeeded with no apptainer anywhere")
	}
	for _, want := range []string{"update --libexec", "host_apptainer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

// Inside a container only libexec's apptainer is used, even where a host_apptainer is visible, and the refusal
// says to install the toolchain on the host, not a fix that is refused there.
func TestResolveInsideContainerNeedsTheToolchain(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	insideContainer = func() bool { return true }
	systemApptainer(t, writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3"))

	_, err := Resolve(overlays)
	if err == nil || !strings.Contains(err.Error(), "update --libexec") || !strings.Contains(err.Error(), "on the host") {
		t.Fatalf("Resolve error = %v, want it to name `update --libexec` on the host", err)
	}
}

// A host_apptainer that clears the floor is used, with or without libexec's.
func TestNormalUsesHostWhenSet(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	hostBin := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, hostBin)

	bin, err := Resolve(overlays)
	if err != nil || bin.Path != hostBin {
		t.Fatalf("Normal with no libexec apptainer = %+v, %v; want the host binary %q", bin, err, hostBin)
	}
	writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	bin, err = Resolve(overlays)
	if err != nil || bin.Path != hostBin {
		t.Errorf("Normal with both = %+v, %v; want the host binary %q", bin, err, hostBin)
	}
}

// A system apptainer below the zstd floor is refused, with the reason.
func TestNormalRefusesOldSystemApptainer(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	systemApptainer(t, writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.3.9"))

	if _, err := Resolve(overlays); err == nil || !strings.Contains(err.Error(), "zstd") {
		t.Fatalf("Normal error = %v, want it to name zstd", err)
	}
}

// With host_apptainer unset, libexec's apptainer is used, and one on PATH never is.
func TestNormalUsesLibexecAndNeverPath(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	onPath := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.2")
	t.Setenv("PATH", filepath.Dir(onPath))
	if _, err := Resolve(overlays); err == nil || !strings.Contains(err.Error(), "host_apptainer") {
		t.Fatalf("Normal with only PATH: err = %v, want a refusal that names host_apptainer", err)
	}

	binPath := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.3")
	bin, err := Resolve(overlays)
	if err != nil || bin.Path != binPath {
		t.Errorf("Normal = %+v, %v; want the libexec binary %q", bin, err, binPath)
	}
}

// A fakeroot exec uses Normal's apptainer: host_apptainer when set, else libexec's.
func TestFakerootUsesNormals(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	if bin, err := Resolve(fakeroot); err != nil || bin.Path != libexecBin {
		t.Fatalf("Fakeroot with host_apptainer unset = %+v, %v; want %q", bin, err, libexecBin)
	}

	hostBin := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, hostBin)
	if bin, err := Resolve(fakeroot); err != nil || bin.Path != hostBin {
		t.Errorf("Fakeroot with host_apptainer set = %+v, %v; want %q", bin, err, hostBin)
	}
}

// A launch with a build's needs uses host_apptainer when set, else libexec's, and never one on PATH.
func TestResolveBuildUsesHostWhenSetElseLibexec(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	onPath := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.2")
	t.Setenv("PATH", filepath.Dir(onPath))
	if _, err := Resolve(build); err == nil || !strings.Contains(err.Error(), "host_apptainer") {
		t.Fatalf("Resolve(build) with only PATH: err = %v, want a refusal that names host_apptainer", err)
	}

	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	if bin, err := Resolve(build); err != nil || bin.Path != libexecBin {
		t.Fatalf("Resolve(build) with libexec = %+v, %v; want the libexec binary %q", bin, err, libexecBin)
	}

	hostBin := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, hostBin)
	if bin, err := Resolve(build); err != nil || bin.Path != hostBin {
		t.Errorf("Resolve(build) with host_apptainer set = %+v, %v; want %q", bin, err, hostBin)
	}
}

// writeHostBuildcfg writes a host apptainer that prints a build configuration for buildcfg.
func writeHostBuildcfg(t *testing.T, suid string, withFakeroot bool) string {
	t.Helper()
	root := t.TempDir()
	if withFakeroot {
		tools := filepath.Join(root, "libexec", "apptainer", "bin")
		if err := os.MkdirAll(tools, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tools, "fakeroot-sysv"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\ncase \"$1\" in buildcfg) echo LIBEXECDIR=" + filepath.Join(root, "libexec") +
		"; echo APPTAINER_SUID_INSTALL=" + suid + " ;; *) echo 'apptainer version 1.5.3' ;; esac\n"
	path := filepath.Join(root, "apptainer")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A non-setuid host apptainer that ships its own fakeroot is replaced by libexec's for a launch with fakeroot, since PATH cannot override it.
// Without libexec's, with a setuid install, or with no fakeroot of its own, the host's is used.
func TestResolveReplacesAHostThatShipsItsOwnFakeroot(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")

	for _, c := range []struct {
		name, suid string
		fakeroot   bool
		want       string
	}{
		{"own fakeroot", "0", true, libexecBin},
		{"setuid", "1", true, ""},
		{"no fakeroot", "0", false, ""},
	} {
		host := writeHostBuildcfg(t, c.suid, c.fakeroot)
		systemApptainer(t, host)
		want := c.want
		if want == "" {
			want = host
		}
		for name, need := range map[string]Need{"build": build, "exec": fakeroot} {
			if bin, err := Resolve(need); err != nil || bin.Path != want {
				t.Errorf("%s, %s: Resolve = %+v, %v; want %q", c.name, name, bin, err, want)
			}
		}
	}

	if err := os.RemoveAll(filepath.Join(dir, "bin")); err != nil {
		t.Fatal(err)
	}
	host := writeHostBuildcfg(t, "0", true)
	systemApptainer(t, host)
	if bin, err := Resolve(build); err != nil || bin.Path != host {
		t.Errorf("no libexec apptainer: Resolve = %+v, %v; want the host's %q", bin, err, host)
	}
}

// A host apptainer below the zstd floor cannot mount what condatainer packs,
// so a launch that mounts overlays refuses rather than failing later inside
// Apptainer's own mount step. A launch that mounts none has no floor.
func TestResolveRefusesAnOldApptainerOnlyForOverlays(t *testing.T) {
	withLibexecTier(t)
	resetApptainerState(t)
	old := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.3.9")
	systemApptainer(t, old)

	for name, need := range map[string]Need{"run": overlays, "exec": fakeroot} {
		if _, err := Resolve(need); err == nil || !strings.Contains(err.Error(), "zstd") || !strings.Contains(err.Error(), old) {
			t.Errorf("%s: err = %v, want it to name zstd and %s", name, err, old)
		}
	}
	if _, err := Resolve(build); err != nil {
		t.Errorf("a build has no floor: %v", err)
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

	bin, err := Resolve(overlays)
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
	bin, err := Resolve(overlays)
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

// Selected names the apptainer and its source without running anything: host_apptainer when set, else libexec's, else none.
// Inside a container it is libexec's only.
func TestSelectedNamesTheSource(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	if path, source := Selected(); path != "" || source != "" {
		t.Fatalf("with nothing installed Selected = %q, %q; want none", path, source)
	}

	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	if path, source := Selected(); path != libexecBin || source != SourceToolchain {
		t.Errorf("Selected = %q, %q; want the toolchain's %q", path, source, libexecBin)
	}

	hostBin := writeFakeBin(t, t.TempDir(), "apptainer", "apptainer version 1.5.3")
	systemApptainer(t, hostBin)
	if path, source := Selected(); path != hostBin || source != SourceHost {
		t.Errorf("Selected = %q, %q; want the host's %q", path, source, hostBin)
	}

	insideContainer = func() bool { return true }
	if path, source := Selected(); path != libexecBin || source != SourceToolchain {
		t.Errorf("inside a container Selected = %q, %q; want the toolchain's %q", path, source, libexecBin)
	}
}

// A writable .img is checked against the fuse2fs the apptainer would mount with: the toolchain's apptainer is exempt,
// and a host apptainer is judged by its own copy, not by the toolchain's.
func TestCheckWritableImgJudgesTheHostsOwnFuse2fs(t *testing.T) {
	dir := withLibexecTier(t)
	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")

	var asked []string
	prev := fuse2fsProblem
	fuse2fsProblem = func(path string) string {
		asked = append(asked, path)
		return "fuse2fs 1.47.0 (" + path + ") has a bug"
	}
	t.Cleanup(func() { fuse2fsProblem = prev })

	if err := (Bin{Path: libexecBin}).CheckWritableImg(); err != nil || len(asked) != 0 {
		t.Fatalf("the toolchain's apptainer: err = %v, asked %v; want it exempt", err, asked)
	}

	host := writeHostBuildcfg(t, "0", false)
	own := filepath.Join(filepath.Dir(host), "libexec", "apptainer", "bin")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	ownFuse2fs := filepath.Join(own, "fuse2fs")
	if err := os.WriteFile(ownFuse2fs, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := (Bin{Path: host}).CheckWritableImg()
	if err == nil || !strings.Contains(err.Error(), "Unset host_apptainer") {
		t.Fatalf("host apptainer: err = %v, want a refusal that says to unset host_apptainer", err)
	}
	if len(asked) != 1 || asked[0] != ownFuse2fs {
		t.Errorf("checked %v, want the host apptainer's own %s", asked, ownFuse2fs)
	}

	if err := os.RemoveAll(filepath.Join(dir, "bin")); err != nil {
		t.Fatal(err)
	}
	if err := (Bin{Path: host}).CheckWritableImg(); err == nil || !strings.Contains(err.Error(), "update --libexec") {
		t.Errorf("with no toolchain: err = %v, want it to name `update --libexec`", err)
	}
}

// A host_apptainer that cannot be run is an error that names it, never a silent switch to the toolchain's apptainer.
// Inside a container the setting is not consulted.
func TestResolveRefusesAnUnusableHostApptainer(t *testing.T) {
	dir := withLibexecTier(t)
	resetApptainerState(t)
	libexecBin := writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	missing := filepath.Join(t.TempDir(), "no-apptainer")
	systemApptainer(t, missing)

	for name, need := range map[string]Need{"run": overlays, "exec": fakeroot, "build": build} {
		_, err := Resolve(need)
		if !errors.Is(err, ErrHostApptainerUnusable) || !strings.Contains(err.Error(), missing) {
			t.Errorf("%s: err = %v, want ErrHostApptainerUnusable naming %s", name, err, missing)
		}
	}
	if path, source := Selected(); path != "" || source != "" {
		t.Errorf("Selected = %q, %q; want none", path, source)
	}

	insideContainer = func() bool { return true }
	if bin, err := Resolve(overlays); err != nil || bin.Path != libexecBin {
		t.Errorf("inside a container Resolve = %+v, %v; want the toolchain's %q", bin, err, libexecBin)
	}
}
