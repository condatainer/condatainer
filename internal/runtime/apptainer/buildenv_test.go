package apptainer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A build runs without any inherited bind variable; other operations keep them.
func TestBuildDropsInheritedBinds(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-apptainer")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range inheritedBindVars {
		t.Setenv(name, "/x")
	}

	run := func(op string) string {
		var out bytes.Buffer
		if err := runApptainerWithOutput(context.Background(), Bin{Path: script}, op, "", false, nil, &out, nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	build := run("build")
	exec := run("exec")
	for _, name := range inheritedBindVars {
		if strings.Contains(build, name+"=") {
			t.Errorf("build kept %s", name)
		}
		if !strings.Contains(exec, name+"=") {
			t.Errorf("exec dropped %s", name)
		}
	}
}

// A launch with fakeroot carries the toolchain's fakeroot first on PATH, and one without it carries nothing.
// With no toolchain, PATH is left alone.
func TestResolveCarriesTheToolchainFakeroot(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	dir := withLibexecTier(t)
	resetApptainerState(t)
	writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	for name, need := range map[string]Need{"exec": fakeroot, "build": build} {
		bin, err := Resolve(need)
		if err != nil || len(bin.Env) != 0 {
			t.Errorf("%s without the toolchain's fakeroot: Resolve = %+v, %v; want no extra environment", name, bin, err)
		}
	}

	helpers := filepath.Join(dir, "lib", "apptainer", "bin")
	if err := os.MkdirAll(helpers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helpers, "fakeroot-sysv"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []string{"PATH=" + helpers + ":/usr/bin"}
	for name, need := range map[string]Need{"exec": fakeroot, "build": build} {
		if bin, err := Resolve(need); err != nil || !slices.Equal(bin.Env, want) {
			t.Errorf("%s: Resolve env = %v, %v; want %v", name, bin.Env, err, want)
		}
	}
	if bin, err := Resolve(overlays); err != nil || len(bin.Env) != 0 {
		t.Errorf("a launch without fakeroot carries %v, %v; want nothing", bin.Env, err)
	}
}

// A launch passes the Bin's environment to the program, after the inherited one so it wins.
func TestLaunchEnvironmentCarriesBinEnv(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-apptainer")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$MARK\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARK", "inherited")
	var out bytes.Buffer
	bin := Bin{Path: script, Env: []string{"MARK=from-bin"}}
	if err := runApptainerWithOutput(context.Background(), bin, "exec", "", false, nil, &out, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "from-bin" {
		t.Errorf("MARK = %q, want from-bin", got)
	}
}

// A failed build whose output shows the fakeroot could not load says so, and what to do.
// Other failures pass through unchanged.
func TestBuildExplainsATooNewFakeroot(t *testing.T) {
	dir := withLibexecTier(t)
	fake := func(stderr string) Bin {
		script := filepath.Join(t.TempDir(), "apptainer")
		body := "#!/bin/sh\ncat >&2 <<'EOF'\n" + stderr + "\nEOF\nexit 255\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return Bin{Path: script}
	}
	glibc := "/.singularity.d/libs/faked: /lib/x86_64-linux-gnu/libc.so.6: version `GLIBC_2.38' not found (required by /.singularity.d/libs/faked)"

	err := Build(context.Background(), "sandbox", "image.def", &BuildOptions{Bin: fake(glibc)})
	if !errors.Is(err, ErrFakerootTooNew) || !strings.Contains(err.Error(), "update --libexec") {
		t.Fatalf("no toolchain: err = %v, want ErrFakerootTooNew naming `update --libexec`", err)
	}
	var failure *ApptainerError
	if !errors.As(err, &failure) {
		t.Errorf("the apptainer error is lost: %v", err)
	}

	writeFakeBin(t, dir, "apptainer", "apptainer version 1.5.4")
	if err := os.MkdirAll(filepath.Join(dir, "lib", "apptainer", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "apptainer", "bin", "fakeroot-sysv"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = Build(context.Background(), "sandbox", "image.def", &BuildOptions{Bin: fake(glibc)})
	if !errors.Is(err, ErrFakerootTooNew) || !strings.Contains(err.Error(), "unset host_apptainer") {
		t.Errorf("with the toolchain: err = %v, want ErrFakerootTooNew naming host_apptainer", err)
	}

	err = Build(context.Background(), "sandbox", "image.def", &BuildOptions{Bin: fake("FATAL: something else")})
	if errors.Is(err, ErrFakerootTooNew) {
		t.Errorf("an unrelated failure was explained as a fakeroot problem: %v", err)
	}
}
