package freeze

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/runtime/apptainer"
	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/testenv"
)

// testHarnessPath is the compiled testdata/mountharness binary, built once
// in TestMain and used for the rest of this file: MountedRun (called by
// nearly every test in this package, directly or via Pack/Unfreeze) re-execs
// os.Executable() into the hidden _mount_job command, and the `go test`
// binary can't play that role since it has no cmd/cobra dispatch at all.
var testHarnessPath string

func TestMain(m *testing.M) {
	if bin, err := buildMountHarness(); err != nil {
		fmt.Fprintln(os.Stderr, "building test harness:", err)
	} else {
		testHarnessPath = bin
		executablePath = func() (string, error) { return bin, nil }
	}
	cleanup := testenv.Isolate()
	// Isolate clears every CNT_* variable, so the apptainer on PATH is named again here.
	if dir := toolchainOfApptainerOnPath(); dir != "" {
		os.Setenv("CNT_LIBEXEC", dir)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func buildMountHarness() (string, error) {
	dir, err := os.MkdirTemp("", "mountharness")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "mountharness")
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/mountharness").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, out)
	}
	return bin, nil
}

func buildTestSqf(t *testing.T) (sqf, mnt string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sqf = filepath.Join(dir, "t.sqf")
	if out, err := exec.Command(hostTool(t, "mksquashfs"), src, sqf, "-no-progress").CombinedOutput(); err != nil {
		t.Fatalf("mksquashfs: %v\n%s", err, out)
	}
	mnt = filepath.Join(dir, "mnt")
	if err := os.MkdirAll(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	return sqf, mnt
}

// fuseRunning reports whether a squashfuse process for sqf exists. The mount lives in a private
// namespace, invisible to the test, so the process is what it watches.
func fuseRunning(sqf string) bool {
	out, _ := exec.Command("pgrep", "-f", "squashfuse.*"+sqf).Output()
	return len(out) != 0
}

// waitFor polls cond until it holds or limit passes, and reports whether it held.
func waitFor(limit time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// toolchainOfApptainerOnPath is the toolchain directory of the apptainer on PATH, or "" when it is not in one.
func toolchainOfApptainerOnPath() string {
	path, err := exec.LookPath("apptainer")
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	dir := filepath.Dir(filepath.Dir(path))
	if _, err := os.Stat(filepath.Join(dir, "MANIFEST.json")); err != nil {
		return ""
	}
	return dir
}

// requireJob skips the test unless the harness was built and an apptainer can run the job.
func requireJob(t *testing.T) {
	t.Helper()
	if testHarnessPath == "" {
		t.Skip("test harness was not built")
	}
	if _, err := apptainer.Resolve(apptainer.Need{Overlays: true}); err != nil {
		t.Skipf("no apptainer available: %v", err)
	}
}

// A ctx cancelled while work is still running must not leave the FUSE
// process behind: Cancel kills the whole process group of the job's command.
func TestMountedRunCancelKillsFuseProcess(t *testing.T) {
	requireJob(t)
	squashfuse, err := FindSquashfuse()
	if err != nil {
		t.Skip("no squashfuse available")
	}

	sqf, mnt := buildTestSqf(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- MountedRun(ctx, []Mount{{Bin: squashfuse, Args: []string{sqf}, Path: mnt}}, []string{sqf}, "sleep 30", execpkg.IO{})
	}()

	// Cancel mid-"work", once the mount is up.
	if !waitFor(10*time.Second, func() bool { return fuseRunning(sqf) }) {
		cancel()
		t.Fatal("FUSE process never appeared")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("MountedRun did not return after cancellation")
	}

	// The kernel needs a moment to reap the killed process group.
	if !waitFor(5*time.Second, func() bool { return !fuseRunning(sqf) }) {
		t.Errorf("FUSE process for %s survived cancellation", sqf)
	}
}

// If the process that called MountedRun dies abruptly (SIGKILL, standing in
// for a crash or an OOM-kill) with no chance to run cmd.Cancel at all, the
// FUSE process must still not be left behind. Pdeathsig cannot be set on the
// apptainer itself, which drops it on entering its namespace, so the job's
// command is one process out and stops the apptainer when it is signalled.
func TestMountedRunSurvivesAbruptDeath(t *testing.T) {
	requireJob(t)
	if _, err := FindSquashfuse(); err != nil {
		t.Skip("no squashfuse available")
	}

	sqf, mnt := buildTestSqf(t)

	// "drive" makes the harness call MountedRun for real, as its own OS
	// process -- standing in for condatainer -- so it can be killed from
	// outside without taking this test binary down with it.
	driver := exec.Command(testHarnessPath, "drive", "squashfuse", sqf, mnt, "sleep 30")
	if err := driver.Start(); err != nil {
		t.Fatalf("starting driver: %v", err)
	}
	driverPid := driver.Process.Pid

	// Wait for the FUSE process to actually come up. The pattern is scoped to
	// this test's own sqf path, never a bare tool name.
	if !waitFor(10*time.Second, func() bool { return fuseRunning(sqf) }) {
		driver.Process.Kill() //nolint:errcheck
		t.Fatal("FUSE process never appeared")
	}

	// Kill only the exact driver pid -- nothing else.
	if err := driver.Process.Kill(); err != nil {
		t.Fatalf("killing driver: %v", err)
	}
	driver.Wait() //nolint:errcheck

	// The kernel delivers Pdeathsig to the job's command, which stops the apptainer.
	if !waitFor(5*time.Second, func() bool { return !fuseRunning(sqf) }) {
		exec.Command("pkill", "-KILL", "-f", "squashfuse.*"+sqf).Run() //nolint:errcheck
		t.Errorf("FUSE process for %s survived the driver's abrupt death (driver pid %d)", sqf, driverPid)
	}
}
