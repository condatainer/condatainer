package hostrun

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// maxSocketPath is the longest path a Unix socket takes.
const maxSocketPath = 107

// ErrSocketPath means the relay's socket path does not fit a Unix socket.
var ErrSocketPath = errors.New("the relay socket path is too long")

// sshBin is the ssh that runs a command on another node. Tests replace it.
var sshBin = "ssh"

var (
	sshOptsMu sync.Mutex
	sshOpts   = map[string][]string{}
	sshVerRe  = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)
)

// sshOptions are the options every remote command runs with.
//   - BatchMode makes a login that needs a prompt fail at once.
//   - accept-new trusts a host key the first time it is seen and refuses a changed one, so a node named in a form the user's known_hosts lacks still connects.
//   - OpenSSH before 7.6 does not know accept-new and fails on it, so it is left out there. The version is read once per ssh.
func sshOptions() []string {
	sshOptsMu.Lock()
	defer sshOptsMu.Unlock()
	if opts, ok := sshOpts[sshBin]; ok {
		return opts
	}
	opts := []string{"-o", "BatchMode=yes"}
	if sshKnowsAcceptNew() {
		opts = append(opts, "-o", "StrictHostKeyChecking=accept-new")
	}
	sshOpts[sshBin] = opts
	return opts
}

// sshKnowsAcceptNew reports whether the ssh is OpenSSH 7.6 or newer.
func sshKnowsAcceptNew() bool {
	out, err := exec.Command(sshBin, "-V").CombinedOutput()
	if err != nil {
		return false
	}
	m := sshVerRe.FindSubmatch(out)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(string(m[1]))
	minor, _ := strconv.Atoi(string(m[2]))
	return major > 7 || (major == 7 && minor >= 6)
}

// Relay is the host side of one launch: a socket in a private directory that runs the commands a container sends.
type Relay struct {
	dir       string
	ln        net.Listener
	defaultOn string
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Open starts the relay of a launch of the given kind, or returns nil when that kind does not open one.
//   - A launch inside a container opens none, since its host parent is the container.
func Open(kind string) (*Relay, error) {
	if !Enabled(kind) || config.IsInsideContainer() {
		return nil, nil
	}
	base := utils.GetTmpDir()
	if err := utils.MkdirAllShared(base); err != nil {
		return nil, err
	}
	return open(base, On())
}

// open starts a relay in a new private directory under base.
func open(base, defaultOn string) (*Relay, error) {
	dir, err := os.MkdirTemp(base, "host-run-")
	if err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, "sock")
	if len(sock) > maxSocketPath {
		os.RemoveAll(dir) //nolint:errcheck
		return nil, fmt.Errorf("%w: %s", ErrSocketPath, sock)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.RemoveAll(dir) //nolint:errcheck
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Relay{dir: dir, ln: ln, defaultOn: defaultOn, cancel: cancel}
	r.wg.Add(1)
	go r.serve(ctx)
	return r, nil
}

// Dir is the host directory that holds the socket, to bind into the container at ContainerDir.
func (r *Relay) Dir() string { return r.dir }

// Close stops the relay, ends the commands still running and removes its directory.
func (r *Relay) Close() {
	if r == nil {
		return
	}
	r.cancel()
	r.ln.Close() //nolint:errcheck
	r.wg.Wait()
	os.RemoveAll(r.dir) //nolint:errcheck
}

func (r *Relay) serve(ctx context.Context) {
	defer r.wg.Done()
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			return
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.handle(ctx, conn)
		}()
	}
}

// handle runs one request. Only the launching user's processes are served.
func (r *Relay) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if uid, err := peerUID(conn); err != nil || uid != uint32(os.Getuid()) {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	reply := func(typ byte, p []byte) {
		mu.Lock()
		defer mu.Unlock()
		writeFrame(conn, typ, p) //nolint:errcheck
	}
	stdout := &frameWriter{send: reply, typ: frameOut}
	stderr := &frameWriter{send: reply, typ: frameErr}
	fail := func(code int, format string, args ...any) {
		fmt.Fprintf(stderr, "host-run: "+format+"\n", args...)
		reply(frameExit, []byte(strconv.Itoa(code)))
	}

	req, stdin, err := readRequest(bufio.NewReader(io.LimitReader(conn, maxRequestLine+MaxStdin+4096)))
	if err != nil {
		fail(1, "%v", err)
		return
	}
	// The client sends nothing more, so a read that returns means it has gone and the command stops.
	go func() {
		var b [1]byte
		conn.Read(b[:]) //nolint:errcheck
		cancel()
	}()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	reply(frameExit, []byte(strconv.Itoa(r.run(ctx, req, stdin, stdout, stderr))))
}

// readRequest reads the request line and its standard input.
func readRequest(br *bufio.Reader) (request, []byte, error) {
	var req request
	line, err := br.ReadBytes('\n')
	if err != nil || len(line) > maxRequestLine {
		return req, nil, errors.New("the request is not complete or is too long")
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return req, nil, fmt.Errorf("the request is not valid: %w", err)
	}
	if len(req.Argv) == 0 || req.Argv[0] == "" {
		return req, nil, errors.New("no command is given")
	}
	if req.StdinLen < 0 || req.StdinLen > MaxStdin {
		return req, nil, fmt.Errorf("standard input is over %d bytes: write the data to a file on the shared file system and run the command on it", MaxStdin)
	}
	stdin := make([]byte, req.StdinLen)
	if _, err := io.ReadFull(br, stdin); err != nil {
		return req, nil, errors.New("standard input is shorter than announced")
	}
	return req, stdin, nil
}

// target is where one command runs.
type target struct {
	node     string // the node to ssh to; "" runs it here
	loginEnv string // when set, CNT_LOGIN_NODE for the command
}

// resolve picks the node of a request: its own, else the launch's default.
//   - localhost, and any name that is this node, run the command here.
//   - login is the recorded login node, and the command then gets CNT_LOGIN_NODE for it.
func (r *Relay) resolve(on string) (target, error) {
	if on == "" {
		on = r.defaultOn
	}
	named := false
	if on == OnLogin {
		node := scheduler.LoginNode()
		if node == "" {
			return target{}, errors.New("no login node is recorded for this job: set CNT_LOGIN_NODE or use --on <node>")
		}
		on, named = node, true
	}
	if on == "" || on == OnLocalhost {
		return target{}, nil
	}
	if err := validateNode(on); err != nil {
		return target{}, err
	}
	t := target{node: on}
	if named {
		t.loginEnv = on
	}
	if isThisNode(on) {
		t.node = ""
	}
	return t, nil
}

// isThisNode reports whether name is this node, compared without the domain.
func isThisNode(name string) bool {
	host, err := os.Hostname()
	return err == nil && shortName(host) == shortName(name)
}

func shortName(name string) string {
	short, _, _ := strings.Cut(name, ".")
	return short
}

// run runs a request's command and returns its exit code.
//   - A leading condatainer is the caller's binary where the host can run it, else the host's own executable.
//     It is never whatever PATH finds.
//   - The command runs directly, never through a shell. Over ssh each word is quoted for the remote shell.
func (r *Relay) run(ctx context.Context, req request, stdin []byte, stdout, stderr io.Writer) int {
	tgt, err := r.resolve(req.On)
	if err != nil {
		fmt.Fprintf(stderr, "host-run: %v\n", err)
		return 1
	}
	argv := hostArgv(req.Argv, req.Exe)

	var cmd *exec.Cmd
	if tgt.node != "" {
		args := append(slices.Clone(sshOptions()), "--", tgt.node, remoteScript(req.Cwd, tgt.loginEnv, req.Argv, req.Exe))
		cmd = exec.CommandContext(ctx, sshBin, args...)
	} else {
		if st, err := os.Stat(req.Cwd); err != nil || !st.IsDir() {
			fmt.Fprintf(stderr, "host-run: the working directory %s does not exist on the host\n", req.Cwd)
			return 1
		}
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = req.Cwd
		cmd.Env = os.Environ()
		if tgt.loginEnv != "" {
			cmd.Env = append(cmd.Env, scheduler.LoginNodeVar+"="+tgt.loginEnv)
		}
	}
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return exitCode(cmd.Run(), argv[0], stderr)
}

// hostArgv is argv with a leading condatainer replaced by the executable the host runs: exe when this
// host can run it, else the host's own executable, so the version that started the job runs.
func hostArgv(argv []string, exe string) []string {
	argv = slices.Clone(argv)
	if argv[0] == "condatainer" {
		argv[0] = utils.SelfPath()
		if runnable(exe) {
			argv[0] = exe
		}
	}
	return argv
}

// runnable reports whether path is an absolute path to an executable file here.
func runnable(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// remoteScript is the one string ssh hands the remote shell: a cd, then the command with every word quoted.
// A leading condatainer runs exe when the remote node has it as an executable file, else the host's own executable.
// The test runs in sh, so the user's login shell, which may not be a POSIX shell, sees only the cd and one word.
func remoteScript(cwd, loginEnv string, argv []string, exe string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cd %s && ", utils.ShellQuote(cwd))
	own := hostArgv(argv, "")
	if argv[0] == "condatainer" && filepath.IsAbs(exe) {
		caller := slices.Clone(own)
		caller[0] = exe
		q := utils.ShellQuote(exe)
		test := fmt.Sprintf("if [ -f %s ] && [ -x %s ]; then %s; else %s; fi", q, q, remoteCommand(loginEnv, caller), remoteCommand(loginEnv, own))
		b.WriteString("sh -c ")
		b.WriteString(utils.ShellQuote(test))
		return b.String()
	}
	b.WriteString(remoteCommand(loginEnv, own))
	return b.String()
}

// remoteCommand is argv with every word quoted, after the login node variable when there is one.
func remoteCommand(loginEnv string, argv []string) string {
	var b strings.Builder
	if loginEnv != "" {
		fmt.Fprintf(&b, "env %s=%s ", scheduler.LoginNodeVar, utils.ShellQuote(loginEnv))
	}
	for i, word := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(utils.ShellQuote(word))
	}
	return b.String()
}

// exitCode turns the end of a command into the code a shell would report.
func exitCode(err error, name string, stderr io.Writer) int {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return 1
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stderr, "host-run: %s: command not found\n", name)
		return 127
	case errors.Is(err, fs.ErrPermission):
		fmt.Fprintf(stderr, "host-run: %s: permission denied\n", name)
		return 126
	}
	fmt.Fprintf(stderr, "host-run: %s: %v\n", name, err)
	return 126
}

// frameWriter sends what is written to it as reply frames of one type.
type frameWriter struct {
	send func(typ byte, p []byte)
	typ  byte
}

func (w *frameWriter) Write(p []byte) (int, error) {
	for rest := p; len(rest) > 0; {
		n := min(len(rest), chunkSize)
		w.send(w.typ, rest[:n])
		rest = rest[n:]
	}
	return len(p), nil
}
