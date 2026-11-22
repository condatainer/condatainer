package apptainer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes an executable shell script and returns its path, standing in for apptainer.
func writeScript(t *testing.T, body string) Bin {
	t.Helper()
	path := filepath.Join(t.TempDir(), "apptainer")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Bin{Path: path}
}

// sysctls points the files the package reads at fresh ones holding apparmor, maxNamespaces and clone, or at none when a value is "".
func sysctls(t *testing.T, apparmor, maxNamespaces, clone string) {
	t.Helper()
	dir := t.TempDir()
	prevA, prevM, prevC := restrictionPath, maxNamespacesPath, unprivilegedClone
	t.Cleanup(func() { restrictionPath, maxNamespacesPath, unprivilegedClone = prevA, prevM, prevC })
	for _, f := range []struct {
		path  *string
		name  string
		value string
	}{
		{&restrictionPath, "apparmor", apparmor},
		{&maxNamespacesPath, "max", maxNamespaces},
		{&unprivilegedClone, "clone", clone},
	} {
		*f.path = filepath.Join(dir, f.name)
		if f.value != "" {
			if err := os.WriteFile(*f.path, []byte(f.value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// appArmor sets AppArmor's sysctl to value, or removes it when value is "", and leaves the other limits off.
func appArmor(t *testing.T, value string) {
	t.Helper()
	sysctls(t, value, "", "")
}

// Every message a refused namespace prints is recognised. The vague one counts only under the restriction.
func TestRefusedOutput(t *testing.T) {
	appArmor(t, "0")
	for _, out := range []string{
		"ERROR  : Could not write info to setgroups: Permission denied",
		"FATAL:   Failed to create mount namespace: mount namespace requires privileges, check Apptainer installation",
	} {
		if !refusedOutput(out) {
			t.Errorf("refusedOutput(%q) = false", out)
		}
	}
	vague := "ERROR  : Failed to create container process: Operation not permitted"
	if refusedOutput(vague) {
		t.Error("the vague message counted with the restriction off")
	}
	appArmor(t, "1")
	if !refusedOutput(vague) {
		t.Error("the vague message did not count with the restriction on")
	}
	if refusedOutput("FATAL:   stat /bin/true: no such file or directory") {
		t.Error("an unrelated failure counted as a refusal")
	}
}

// A refused launch is ErrUsernsBlocked, names the apptainer, and is not an ApptainerError.
// The AppArmor advice is in the message only while the restriction is on.
func TestExecRefusedNamespaceIsErrUsernsBlocked(t *testing.T) {
	bin := writeScript(t, `echo "ERROR  : Could not write info to setgroups: Permission denied" >&2; exit 255`)
	launch := func() error {
		return Exec(context.Background(), "image.sif", []string{"true"}, &ExecOptions{Bin: bin, Stderr: &bytes.Buffer{}})
	}
	advice := []string{"AppArmor", "allowlist", "apparmor_restrict_unprivileged_userns"}

	appArmor(t, "")
	err := launch()
	if !errors.Is(err, ErrUsernsBlocked) || !strings.Contains(err.Error(), bin.Path) {
		t.Fatalf("err = %v, want ErrUsernsBlocked naming %s", err, bin.Path)
	}
	var failure *ApptainerError
	if errors.As(err, &failure) {
		t.Errorf("a refusal is also an ApptainerError, so its exit status would be passed on")
	}
	for _, text := range advice {
		if strings.Contains(err.Error(), text) {
			t.Errorf("restriction off: err = %v, want no mention of %q", err, text)
		}
	}

	appArmor(t, "1")
	err = launch()
	for _, text := range advice {
		if err == nil || !strings.Contains(err.Error(), text) {
			t.Errorf("restriction on: err = %v, want it to mention %q", err, text)
		}
	}
}

// With stderr on a terminal there is no output to read. Under the restriction a failed launch is settled by a launch on an empty directory.
func TestExecOnTerminalIsSettledByProbe(t *testing.T) {
	bin := writeScript(t, `case "$*" in *"/bin/true"*) echo "ERROR  : Could not write info to setgroups: Permission denied" >&2 ;; esac; exit 1`)
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	run := func() error {
		return runApptainerWithOutput(context.Background(), bin, "exec", "image.sif", false, nil, nil, stderr, nil, nil, 0, "exec", "image.sif", "cmd")
	}

	appArmor(t, "1")
	if err := run(); !errors.Is(err, ErrUsernsBlocked) {
		t.Errorf("restriction on: err = %v, want ErrUsernsBlocked", err)
	}
	appArmor(t, "0")
	var failure *ApptainerError
	if err := run(); !errors.As(err, &failure) || failure.ExitCode() != 1 {
		t.Errorf("restriction off: err = %v, want an ApptainerError with exit status 1", err)
	}
}

// Any other failure stays an ApptainerError with the container command's exit status.
func TestExecOtherFailureKeepsItsExitStatus(t *testing.T) {
	appArmor(t, "1")
	bin := writeScript(t, `echo boom >&2; exit 3`)

	err := Exec(context.Background(), "image.sif", []string{"false"}, &ExecOptions{Bin: bin, Stderr: &bytes.Buffer{}})
	var failure *ApptainerError
	if !errors.As(err, &failure) || failure.ExitCode() != 3 || errors.Is(err, ErrUsernsBlocked) {
		t.Fatalf("err = %v, want an ApptainerError with exit status 3 and not ErrUsernsBlocked", err)
	}
}

// Each common cause gets its own advice, and a refusal with no cause shown says only to allow namespaces.
func TestBlockedErrorNamesTheCause(t *testing.T) {
	resetApptainerState(t)
	withLibexecTier(t)
	bin := Bin{Path: "/x/apptainer"}
	for _, c := range []struct {
		name                           string
		apparmor, maxNamespaces, clone string
		want                           string
	}{
		{"apparmor", "1", "", "", "AppArmor allowlist"},
		{"no namespaces left", "0", "0", "", "user.max_user_namespaces"},
		{"clone disabled", "", "", "0", "kernel.unprivileged_userns_clone=1"},
		{"no cause shown", "0", "15076", "1", "allow unprivileged user namespaces"},
	} {
		sysctls(t, c.apparmor, c.maxNamespaces, c.clone)
		if err := blockedError(bin); !errors.Is(err, ErrUsernsBlocked) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want ErrUsernsBlocked mentioning %q", c.name, err, c.want)
		}
	}
}

// A refused toolchain apptainer is told about an apptainer on PATH only when that one proves it works.
// A refused host_apptainer, a refused PATH apptainer and a restriction-free system get no suggestion.
func TestBlockedErrorSuggestsAWorkingApptainer(t *testing.T) {
	resetApptainerState(t)
	dir := withLibexecTier(t)
	toolchain := Bin{Path: writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")}
	sysctls(t, "1", "", "")

	candidate := func(version string, runs bool) string {
		onPath := t.TempDir()
		body := "case \"$1\" in --version) echo 'apptainer version " + version + "' ;; exec) exit " + map[bool]string{true: "0", false: "1"}[runs] + " ;; esac"
		script := filepath.Join(onPath, "apptainer")
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", onPath)
		return script
	}

	script := candidate("1.5.3", true)
	// The toolchain's own apptainer ahead of it on PATH is skipped, not taken as the candidate.
	t.Setenv("PATH", filepath.Dir(toolchain.Path)+":"+filepath.Dir(script))
	err := blockedError(toolchain)
	if err == nil || !strings.Contains(err.Error(), "host_apptainer "+script) || !strings.Contains(err.Error(), "Or an administrator can") {
		t.Fatalf("a working candidate: err = %v, want the command that selects %s", err, script)
	}

	for name, c := range map[string]struct {
		version string
		runs    bool
	}{"refused too": {"1.5.3", false}, "below the floor": {"1.3.9", true}} {
		candidate(c.version, c.runs)
		if err := blockedError(toolchain); err == nil || strings.Contains(err.Error(), "host_apptainer") {
			t.Errorf("%s: err = %v, want no suggestion", name, err)
		}
	}

	candidate("1.5.3", true)
	if err := blockedError(Bin{Path: "/elsewhere/apptainer"}); strings.Contains(err.Error(), "condatainer config set") {
		t.Errorf("a refused host apptainer got a suggestion: %v", err)
	}
	sysctls(t, "0", "", "")
	if err := blockedError(toolchain); strings.Contains(err.Error(), "condatainer config set") {
		t.Errorf("with no restriction shown got a suggestion: %v", err)
	}
}
