package freeze

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// EnvSentinelWork is the env var MountedRun uses to hand the work script to
// the re-exec'd sentinel process; cmd's hidden `_mount_sentinel` command
// reads the same name. fuseBin, mnt, and fuseArgs travel as plain CLI args
// instead (os/exec rejects any env var value containing a NUL byte, which
// ruled out joining fuseArgs into one; args need no joining or escaping at
// all, so everything that isn't a multi-line script goes there).
const EnvSentinelWork = "CNT_MOUNT_WORK"

// RunSentinel is the body of the hidden `_mount_sentinel` command that MountedRun re-execs into.
//   - It never escalates privilege, so its Pdeathsig (SIGTERM) keeps working.
//   - On it, the sentinel kills its own process group, taking the namespaced unshare, bash and FUSE tree with it.
//   - That covers condatainer being killed mid-mount, not the sentinel itself being killed.
func RunSentinel(fuseBin, mnt, work string, fuseArgs []string) error {
	pgid, err := syscall.Getpgid(0)
	if err != nil {
		return fmt.Errorf("reading own process group: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating condatainer binary: %w", err)
	}

	var quoted []string
	for _, a := range append(append([]string{}, fuseArgs...), mnt) {
		quoted = append(quoted, shellQuote(a))
	}
	// Work runs in a subshell so its exit status is kept while the FUSE process
	// is stopped afterwards.
	script := fmt.Sprintf(`set -o pipefail
%s _mount_exec -- %s -f %s &
fpid=$!
for i in $(seq 1 50); do grep -qF ' %s ' /proc/mounts && break; sleep 0.1; done
grep -qF ' %s ' /proc/mounts || { echo "mount never appeared" >&2; kill $fpid 2>/dev/null; wait $fpid 2>/dev/null; exit 1; }
( %s )
rc=$?
kill $fpid 2>/dev/null
wait $fpid 2>/dev/null
exit $rc
`, shellQuote(self), shellQuote(fuseBin), strings.Join(quoted, " "), mnt, mnt, work)

	cmd := exec.Command("unshare", "--mount", "--user", "--map-root-user", "--", "/bin/bash", "-c", script)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Join the sentinel's own group instead of starting a fresh one: both the
	// SIGTERM path below and MountedRun's own cmd.Cancel (a still-alive
	// condatainer choosing to cancel) kill by group, and need bash/FUSE in it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting unshare: %w", err)
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGTERM)
	defer signal.Stop(sigc)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-sigc:
		// condatainer is gone; nothing further to report to. Best-effort:
		// take the whole group down, including this process.
		syscall.Kill(-pgid, syscall.SIGKILL) //nolint:errcheck
		<-done
		return fmt.Errorf("mount sentinel: condatainer exited before the mount finished")
	case err := <-done:
		return err
	}
}

// ExecWithoutDACOverride replaces this process with bin, which keeps the namespace root's other capabilities but not the two that skip file permission checks.
//   - Namespace root otherwise ignores a cleared write bit, so a FUSE server would open a write-protected image read-write.
//   - Without them the server sees the permission bits as the image's owner does, and mounts a write-protected image read-only.
//   - The capabilities are dropped from the bounding set, which applies to the thread that execs.
func ExecWithoutDACOverride(bin string, args []string) error {
	runtime.LockOSThread()
	for _, capability := range []uintptr{unix.CAP_DAC_OVERRIDE, unix.CAP_DAC_READ_SEARCH} {
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, capability, 0, 0, 0); err != nil {
			return fmt.Errorf("dropping capability %d: %w", capability, err)
		}
	}
	return unix.Exec(bin, append([]string{bin}, args...), os.Environ())
}
