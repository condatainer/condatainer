package freeze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/runtime/apptainer"
	execpkg "github.com/condatainer/condatainer/internal/runtime/exec"
	"github.com/condatainer/condatainer/internal/utils"
)

// jobStopGrace is how long the job's apptainer gets to stop after a cancel before it is killed.
const jobStopGrace = 5 * time.Second

// jobSpec is what the hidden `_mount_job` command needs to run one apptainer job.
type jobSpec struct {
	Root   string   // the sandbox directory used as the container root
	Work   string   // the script bash runs in the job
	Binds  []string // host paths made visible in the job, as `path` or `host:container`
	Mounts []string // one --fusemount specification per mount, in order
}

// systemDirs are the host directories every job sees, so bash and the tools find their libraries.
var systemDirs = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"}

// systemFiles are bound as the files themselves. Binding their directory would bring in the host's /etc,
// where /etc/resolv.conf can be a link into /run, which the job's root does not have.
var systemFiles = []string{"/etc/passwd", "/etc/group"}

// runJob makes the mounts in one apptainer job, started through the hidden `_mount_job` command.
//   - The command is a separate process that never enters a namespace, so it keeps its Pdeathsig. The apptainer it starts does not.
//   - Killing its process group ends the command, the apptainer, the FUSE servers and the work together.
func runJob(ctx context.Context, mounts []Mount, binds []string, work string, io execpkg.IO) error {
	self, err := executablePath()
	if err != nil {
		return fmt.Errorf("locating condatainer binary: %w", err)
	}
	scratch := utils.GetTmpDir()
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("stage job: %w", err)
	}
	dir, err := os.MkdirTemp(scratch, "cnt-job-")
	if err != nil {
		return fmt.Errorf("stage job: %w", err)
	}
	defer os.RemoveAll(dir)

	spec, err := buildJob(dir, mounts, binds, work)
	if err != nil {
		return err
	}
	specPath := filepath.Join(dir, "job.json")
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		return fmt.Errorf("stage job: %w", err)
	}

	cmd := exec.CommandContext(ctx, self, "_mount_job", specPath)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = io.Stdin, io.Stdout, io.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Run()
}

// buildJob lays out the job in dir: its root, its script, and the specification of the apptainer launch.
//   - A path with a space or a comma cannot be written into a --fusemount specification, so it is reached through a link in dir.
func buildJob(dir string, mounts []Mount, binds []string, work string) (jobSpec, error) {
	root := filepath.Join(dir, "root")
	for _, sub := range []string{"proc", "dev", "tmp", "var/tmp", "etc", "root", "home", "mnt"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return jobSpec{}, fmt.Errorf("stage job root: %w", err)
		}
	}
	workPath := filepath.Join(dir, "work.sh")
	if err := os.WriteFile(workPath, []byte(work), 0o755); err != nil {
		return jobSpec{}, fmt.Errorf("stage job script: %w", err)
	}

	links := 0
	plain := func(word string) (string, error) {
		if !strings.ContainsAny(word, " \t,") {
			return word, nil
		}
		if _, err := os.Lstat(word); err != nil {
			return "", fmt.Errorf("%q has a space or a comma and is not a path, so it cannot be passed to a FUSE mount", word)
		}
		link := filepath.Join(dir, fmt.Sprintf("link%d", links))
		links++
		if err := os.Symlink(word, link); err != nil {
			return "", fmt.Errorf("stage job link: %w", err)
		}
		return link, nil
	}

	spec := jobSpec{Root: root, Work: workPath}
	wanted := slices.Clone(binds)
	wanted = append(wanted, dir)
	for _, m := range mounts {
		words := append([]string{m.Bin}, m.Args...)
		words = append(words, m.Path)
		for i, w := range words {
			p, err := plain(w)
			if err != nil {
				return jobSpec{}, err
			}
			words[i] = p
		}
		spec.Mounts = append(spec.Mounts, "container:"+strings.Join(words, " "))
		wanted = append(wanted, m.Bin, m.Path)
		for _, a := range m.Args {
			if filepath.IsAbs(a) {
				wanted = append(wanted, a)
			}
		}
	}
	binds, err := jobBinds(wanted)
	if err != nil {
		return jobSpec{}, err
	}
	spec.Binds = binds
	return spec, nil
}

// jobBinds lists what the job binds: the system directories, the toolchain, the directories on PATH, and wanted.
//   - A file is bound as its directory, and a path that does not exist yet as its nearest parent that does.
//   - A path under one already listed is dropped.
//   - A path in wanted that a bind cannot carry is an error. One of the ambient directories is skipped.
func jobBinds(wanted []string) ([]string, error) {
	ambient := slices.Clone(systemDirs)
	if dir, ok := libexec.Dir(); ok {
		ambient = append(ambient, dir)
	}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(d) {
			ambient = append(ambient, d)
		}
	}

	var entries []string
	add := func(p string, required bool) error {
		e, ok, err := bindEntry(p, false)
		switch {
		case err != nil && required:
			return err
		case err != nil || !ok || slices.Contains(entries, e):
			return nil
		}
		entries = append(entries, e)
		return nil
	}
	for _, p := range ambient {
		if err := add(p, false); err != nil {
			return nil, err
		}
	}
	for _, p := range systemFiles {
		if e, ok, err := bindEntry(p, true); err == nil && ok && !slices.Contains(entries, e) {
			entries = append(entries, e)
		}
	}
	for _, p := range wanted {
		if err := add(p, true); err != nil {
			return nil, err
		}
	}
	return slices.DeleteFunc(slices.Clone(entries), func(e string) bool {
		for _, other := range entries {
			if other != e && !strings.Contains(other, ":") && !strings.Contains(e, ":") && strings.HasPrefix(e, strings.TrimSuffix(other, "/")+"/") {
				return true
			}
		}
		return false
	}), nil
}

// bindEntry is the bind for one path, and false when there is nothing to bind.
//   - A path reached through a link is bound as the real path at the link's own name.
//   - A file is bound as its directory, unless keepFile says to bind the file.
func bindEntry(p string, keepFile bool) (string, bool, error) {
	p = filepath.Clean(p)
	for p != "/" && p != "." {
		if info, err := os.Lstat(p); err == nil {
			if !info.IsDir() && !keepFile {
				if target, err := os.Stat(p); err != nil || !target.IsDir() {
					p = filepath.Dir(p)
				}
			}
			break
		}
		p = filepath.Dir(p)
	}
	if p == "/" || p == "." {
		return "", false, nil
	}
	real := p
	if r, err := filepath.EvalSymlinks(p); err == nil {
		real = r
	}
	for _, path := range []string{p, real} {
		if strings.ContainsAny(path, ",:") {
			return "", false, fmt.Errorf("%q has a comma or a colon, which a bind cannot carry", path)
		}
	}
	if real != p {
		return real + ":" + p, true, nil
	}
	return p, true, nil
}

// RunJob runs the job a specification file describes: one apptainer launch with the mounts as --fusemount.
// It is the body of the hidden `_mount_job` command.
//   - The job's own warnings are dropped, since the root is a bare directory and apptainer complains about what it lacks.
//   - It drops every inherited bind. It binds all it needs, and its bare root has no mount point for a file a container binds.
//   - A cancel stops the apptainer, which ends the FUSE servers and the work with it.
func RunJob(ctx context.Context, specPath string) error {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("reading the job: %w", err)
	}
	var spec jobSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("reading the job: %w", err)
	}
	bin, err := apptainer.Resolve(apptainer.Need{Overlays: true})
	if err != nil {
		return err
	}

	flags := []string{"--no-home", "--pwd", "/"}
	for _, m := range spec.Mounts {
		flags = append(flags, "--fusemount", m)
	}
	stderr := apptainer.NewApptainerFilter(os.Stderr, apptainer.ApptainerMessageNonErrorPrefixes...)
	defer stderr.Flush()

	return apptainer.Exec(ctx, spec.Root, []string{"/bin/bash", spec.Work}, &apptainer.ExecOptions{
		Bin: bin, Bind: spec.Binds, NoInheritedBinds: true, Additional: flags,
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: stderr, StopGrace: jobStopGrace,
	})
}

// JobExitCode is the exit status of the script a failed RunJob ran, when it has one.
func JobExitCode(err error) (int, bool) {
	var failure *apptainer.ApptainerError
	if errors.As(err, &failure) && failure.ExitCode() >= 0 {
		return failure.ExitCode(), true
	}
	return 0, false
}
