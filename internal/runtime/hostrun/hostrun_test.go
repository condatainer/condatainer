package hostrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
)

// startRelay opens a relay in a short directory, since a socket path is at most 107 bytes.
func startRelay(t *testing.T, defaultOn string) *Relay {
	t.Helper()
	base, err := os.MkdirTemp("", "hr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	r, err := open(base, defaultOn)
	if errors.Is(err, ErrSocketPath) {
		t.Skip("the temp directory is too long for a socket")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

type result struct {
	code           int
	stdout, stderr string
}

// call sends argv through r from the test's directory.
func call(t *testing.T, r *Relay, c Call) result {
	t.Helper()
	var out, errOut bytes.Buffer
	c.Sock = filepath.Join(r.Dir(), "sock")
	c.Stdout, c.Stderr = &out, &errOut
	if c.Cwd == "" {
		c.Cwd = t.TempDir()
	}
	code, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result{code, out.String(), errOut.String()}
}

func TestRelayRunsACommandAndKeepsTheStreamsApart(t *testing.T) {
	r := startRelay(t, "")
	got := call(t, r, Call{Argv: []string{"sh", "-c", "echo out; echo err >&2; exit 3"}})
	if got.code != 3 || got.stdout != "out\n" || got.stderr != "err\n" {
		t.Errorf("got %+v, want exit 3, stdout out, stderr err", got)
	}
	if got := call(t, r, Call{Argv: []string{"ls", "/"}}); got.code != 0 || got.stdout == "" {
		t.Errorf("an ordinary command: %+v", got)
	}
}

func TestRelayStartsInTheWorkingDirectory(t *testing.T) {
	r := startRelay(t, "")
	dir := t.TempDir()
	got := call(t, r, Call{Argv: []string{"pwd"}, Cwd: dir})
	want, _ := filepath.EvalSymlinks(dir)
	if strings.TrimSpace(got.stdout) != want && strings.TrimSpace(got.stdout) != dir {
		t.Errorf("pwd = %q, want %q", got.stdout, dir)
	}
}

func TestStdinIsSentOnlyWhenGiven(t *testing.T) {
	r := startRelay(t, "")
	if got := call(t, r, Call{Argv: []string{"cat"}, Stdin: strings.NewReader("data")}); got.stdout != "data" {
		t.Errorf("with stdin: %+v", got)
	}
	if got := call(t, r, Call{Argv: []string{"cat"}}); got.code != 0 || got.stdout != "" {
		t.Errorf("without stdin the command sees it empty: %+v", got)
	}
}

func TestStdinOverTheLimitIsRefusedBeforeSending(t *testing.T) {
	c := Call{Sock: "/nonexistent", Argv: []string{"cat"}, Stdin: bytes.NewReader(make([]byte, MaxStdin+1))}
	if _, err := c.Run(t.Context()); !errors.Is(err, ErrStdinTooLarge) {
		t.Errorf("err = %v, want ErrStdinTooLarge", err)
	}
}

func TestExitCodesFollowAShell(t *testing.T) {
	r := startRelay(t, "")
	if got := call(t, r, Call{Argv: []string{"no-such-command-here"}}); got.code != 127 || !strings.Contains(got.stderr, "command not found") {
		t.Errorf("missing command: %+v", got)
	}
	plain := filepath.Join(t.TempDir(), "notexec")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := call(t, r, Call{Argv: []string{plain}}); got.code != 126 {
		t.Errorf("not executable: %+v", got)
	}
	if got := call(t, r, Call{Argv: []string{"sh", "-c", "kill -TERM $$"}}); got.code != 143 {
		t.Errorf("killed by a signal: %+v", got)
	}
}

func TestAMissingWorkingDirectoryIsAnErrorThatNamesIt(t *testing.T) {
	r := startRelay(t, "")
	got := call(t, r, Call{Argv: []string{"pwd"}, Cwd: "/only/in/the/image"})
	if got.code == 0 || !strings.Contains(got.stderr, "/only/in/the/image") {
		t.Errorf("got %+v", got)
	}
}

func TestACommandStopsWhenTheClientGoes(t *testing.T) {
	r := startRelay(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	c := Call{Sock: filepath.Join(r.Dir(), "sock"), Argv: []string{"sleep", "30"}, Cwd: t.TempDir(), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if _, err := c.Run(ctx); err == nil {
		t.Fatal("expected the call to end with the context")
	}
	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the relay still waits for the command after the client went")
	}
}

func TestCloseRemovesTheDirectory(t *testing.T) {
	r := startRelay(t, "")
	dir := r.Dir()
	r.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("directory still there: %v", err)
	}
}

func TestOpenRefusesAPathThatDoesNotFitASocket(t *testing.T) {
	base := filepath.Join(t.TempDir(), strings.Repeat("a", 120))
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := open(base, ""); !errors.Is(err, ErrSocketPath) {
		t.Fatalf("err = %v, want ErrSocketPath", err)
	}
	if left, _ := os.ReadDir(base); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

func TestResolveChoosesTheNode(t *testing.T) {
	host, err := os.Hostname()
	if err != nil || validateNode(host) != nil {
		t.Skipf("hostname %q is not a plain host name", host)
	}
	t.Setenv("SLURM_JOB_ID", "1")
	t.Setenv(scheduler.LoginNodeVar, "login02")
	for _, tc := range []struct {
		name, defaultOn, on string
		want                target
		wantErr             bool
	}{
		{name: "default is here", want: target{}},
		{name: "localhost", on: "localhost", want: target{}},
		{name: "another node", on: "node7", want: target{node: "node7"}},
		{name: "this node by name", on: host, want: target{}},
		{name: "this node without its domain", on: strings.Split(host, ".")[0] + ".example.org", want: target{}},
		{name: "the key's default", defaultOn: "node7", want: target{node: "node7"}},
		{name: "the call beats the key", defaultOn: "node7", on: "node8", want: target{node: "node8"}},
		{name: "login", on: "login", want: target{node: "login02", loginEnv: "login02"}},
		{name: "login from the key", defaultOn: "login", want: target{node: "login02", loginEnv: "login02"}},
		{name: "an option is not a node", on: "-oProxyCommand=x", wantErr: true},
		{name: "a space is not a node", on: "a b", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&Relay{defaultOn: tc.defaultOn}).resolve(tc.on)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("resolve = %+v, %v; want %+v, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}

	t.Run("login with none recorded in a job", func(t *testing.T) {
		t.Setenv(scheduler.LoginNodeVar, "")
		os.Unsetenv(scheduler.LoginNodeVar)
		if _, err := (&Relay{}).resolve("login"); err == nil || !strings.Contains(err.Error(), "CNT_LOGIN_NODE") {
			t.Errorf("err = %v, want one that names CNT_LOGIN_NODE", err)
		}
	})
}

func TestLoginOnTheLoginNodeRunsHereWithTheVariableSet(t *testing.T) {
	host, err := os.Hostname()
	if err != nil || validateNode(host) != nil {
		t.Skipf("hostname %q is not a plain host name", host)
	}
	for _, k := range []string{"SLURM_JOB_ID", "PBS_JOBID", "LSB_JOBID", "_CONDOR_JOB_AD", scheduler.LoginNodeVar} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	r := startRelay(t, "")
	got := call(t, r, Call{On: "login", Argv: []string{"sh", "-c", "echo $CNT_LOGIN_NODE"}})
	if strings.TrimSpace(got.stdout) != host {
		t.Errorf("CNT_LOGIN_NODE = %q, want %q", got.stdout, host)
	}
}

// remoteScript is read by the remote shell, so a word with a space, a quote or a shell character stays one word.
func TestRemoteScriptKeepsEveryWordWhole(t *testing.T) {
	words := []string{"printf", "[%s]", "a b", "it's", "$(touch pwned)", ";", "`id`", "*"}
	dir := t.TempDir()
	out, err := exec.Command("sh", "-c", remoteScript(dir, "", words)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := "[a b][it's][$(touch pwned)][;][`id`][*]"; string(out) != want {
		t.Errorf("printed %q, want %q", out, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("a word ran as a command")
	}
}

func TestRemoteScriptSetsTheLoginNodeAndChangesDirectory(t *testing.T) {
	dir := t.TempDir()
	out, err := exec.Command("sh", "-c", remoteScript(dir, "login02", []string{"sh", "-c", "echo $CNT_LOGIN_NODE; pwd"})).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 || lines[0] != "login02" {
		t.Errorf("printed %q", out)
	}
}

// A fake ssh answers -V with the given version line, records how it is called, and runs the remote string locally.
func fakeSSH(t *testing.T, version string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = -V ]; then echo " + utils.ShellQuote(version) + " >&2; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" > " + utils.ShellQuote(argsFile) + "\n" +
		"for last; do :; done\nexec sh -c \"$last\"\n"
	path := filepath.Join(dir, "ssh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := sshBin
	sshBin = path
	t.Cleanup(func() { sshBin = old })
	return argsFile
}

func TestAnotherNodeRunsOverSSHInBatchMode(t *testing.T) {
	argsFile := fakeSSH(t, "OpenSSH_9.6p1 Ubuntu, OpenSSL 3.0.13")
	r := startRelay(t, "")
	dir := t.TempDir()
	got := call(t, r, Call{On: "node7", Cwd: dir, Argv: []string{"printf", "%s", "a b"}})
	if got.code != 0 || got.stdout != "a b" {
		t.Fatalf("got %+v", got)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 7 || strings.Join(lines[:6], " ") != "-o BatchMode=yes -o StrictHostKeyChecking=accept-new -- node7" || !strings.HasPrefix(lines[6], "cd ") {
		t.Errorf("ssh was called with %q", lines)
	}
}

func TestLoginOverSSHGivesTheCommandTheLoginNode(t *testing.T) {
	fakeSSH(t, "OpenSSH_9.6p1")
	t.Setenv("SLURM_JOB_ID", "1")
	t.Setenv(scheduler.LoginNodeVar, "login02")
	r := startRelay(t, "login")
	got := call(t, r, Call{Argv: []string{"sh", "-c", "echo $CNT_LOGIN_NODE"}})
	if strings.TrimSpace(got.stdout) != "login02" {
		t.Errorf("got %+v", got)
	}
}

func TestALeadingCondatainerIsTheHostExecutable(t *testing.T) {
	got := hostArgv([]string{"condatainer", "run", "x.sh"})
	if got[0] != utils.SelfPath() || got[1] != "run" {
		t.Errorf("hostArgv = %q", got)
	}
	if got := hostArgv([]string{"sbatch", "condatainer"}); got[0] != "sbatch" || got[1] != "condatainer" {
		t.Errorf("only the first word is replaced: %q", got)
	}
}

func TestFramesRoundTripAndRefuseAnOversizedOne(t *testing.T) {
	var b bytes.Buffer
	if err := writeFrame(&b, frameOut, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := readFrame(&b)
	if err != nil || typ != frameOut || string(data) != "hello" {
		t.Errorf("round trip = %q, %q, %v", typ, data, err)
	}
	if _, _, err := readFrame(bytes.NewReader([]byte{frameOut, 0xff, 0xff, 0xff, 0xff})); err == nil {
		t.Error("an oversized frame was accepted")
	}
}

func TestLaunchSettings(t *testing.T) {
	if !Enabled(KindHelper) || Enabled(KindExec) || Enabled(KindRun) {
		t.Error("by default only a helper launch opens the relay")
	}
	settingstest.OverrideList(t, "host_run.launch", KindExec, KindRun)
	if Enabled(KindHelper) || !Enabled(KindExec) || !Enabled(KindRun) {
		t.Error("exec and run are enabled, helper is not")
	}
	settingstest.OverrideList(t, "host_run.launch", KindNone, KindHelper)
	if Enabled(KindHelper) || Enabled(KindExec) {
		t.Error("none turns every launch off")
	}
}

func TestOpenReturnsNothingForAKindThatIsOff(t *testing.T) {
	settingstest.OverrideList(t, "host_run.launch", KindNone)
	if r, err := Open(KindHelper); r != nil || err != nil {
		t.Errorf("Open = %v, %v; want nothing", r, err)
	}
}

func TestOnSettingValidation(t *testing.T) {
	for _, ok := range []string{"localhost", "login", "login02", "node-7.example.org"} {
		if err := validateOn(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "a b", "a;b", "$(x)"} {
		if err := validateOn(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, bad := range []string{"", "all", "Helper"} {
		if err := validateKind(bad); err == nil {
			t.Errorf("launch %q accepted", bad)
		}
	}
}

// A launch inside a container has the container as its host parent, so it opens no relay of its own.
func TestALaunchInsideAContainerOpensNoRelay(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "1")
	settingstest.OverrideList(t, "host_run.launch", KindHelper, KindExec, KindRun)
	for _, kind := range []string{KindHelper, KindExec, KindRun} {
		if r, err := Open(kind); r != nil || err != nil {
			t.Errorf("Open(%q) = %v, %v; want nothing", kind, r, err)
		}
	}
}

// A command with a slash is a path: absolute as given, relative to the working directory the client sent.
func TestACommandWithASlashRunsAsAPath(t *testing.T) {
	r := startRelay(t, "")
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran $1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := call(t, r, Call{Argv: []string{script, "abs"}}); got.code != 0 || got.stdout != "ran abs\n" {
		t.Errorf("absolute path: %+v", got)
	}
	if got := call(t, r, Call{Argv: []string{"./run.sh", "rel"}, Cwd: dir}); got.code != 0 || got.stdout != "ran rel\n" {
		t.Errorf("relative path: %+v", got)
	}
}

// What CondaTainer binds at the root of a container sits under one hidden directory.
func TestContainerPathsAreUnderTheContainerRoot(t *testing.T) {
	for _, path := range []string{ContainerDir, ContainerSock} {
		if !strings.HasPrefix(path, config.ContainerRoot+"/") {
			t.Errorf("%s is not under %s", path, config.ContainerRoot)
		}
	}
	if filepath.Dir(ContainerSock) != ContainerDir {
		t.Errorf("the socket %s is not in %s", ContainerSock, ContainerDir)
	}
}

// OpenSSH before 7.6 does not know accept-new and fails on it, so the option is left out there.
func TestAcceptNewIsLeftOutForAnOldSSH(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"OpenSSH_9.6p1 Ubuntu-3ubuntu13", true},
		{"OpenSSH_8.0p1, OpenSSL 1.1.1k", true},
		{"OpenSSH_7.6p1, OpenSSL 1.0.2n", true},
		{"OpenSSH_7.4p1, OpenSSL 1.0.2k-fips", false},
		{"OpenSSH_6.6.1p1", false},
		{"not an ssh version line", false},
	} {
		fakeSSH(t, tc.version)
		got := slices.Contains(sshOptions(), "StrictHostKeyChecking=accept-new")
		if got != tc.want {
			t.Errorf("%q: accept-new = %v, want %v", tc.version, got, tc.want)
		}
		if !slices.Contains(sshOptions(), "BatchMode=yes") {
			t.Errorf("%q: BatchMode is missing", tc.version)
		}
	}
}
