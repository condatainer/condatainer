package apptainer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/utils"
)

// ErrUsernsBlocked reports a launch for which the system refused a user namespace.
var ErrUsernsBlocked = errors.New("the system blocks user namespaces")

// The sysctl files that limit unprivileged user namespaces. Tests point them elsewhere.
var (
	restrictionPath   = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"
	maxNamespacesPath = "/proc/sys/user/max_user_namespaces"
	unprivilegedClone = "/proc/sys/kernel/unprivileged_userns_clone"
)

// restrictionKind says which common limit on user namespaces is in force.
type restrictionKind int

const (
	noRestriction restrictionKind = iota
	appArmorRestriction
	noNamespacesLeft
	cloneDisabled
)

func sysctlValue(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// restriction reports which common limit on user namespaces is in force, by reading the sysctls.
//   - AppArmor's restriction, a namespace limit of 0, and a kernel that disables unprivileged namespaces are the cases named.
//   - Another cause, such as SELinux or a container runtime's filter, leaves the answer to the refusal text.
func restriction() restrictionKind {
	switch {
	case sysctlValue(restrictionPath) == "1":
		return appArmorRestriction
	case sysctlValue(maxNamespacesPath) == "0":
		return noNamespacesLeft
	case sysctlValue(unprivilegedClone) == "0":
		return cloneDisabled
	}
	return noRestriction
}

// probeTimeout bounds the launch that tests whether an apptainer is refused.
const probeTimeout = 15 * time.Second

// refusalMessages are what a refused user namespace prints, in lower case.
var refusalMessages = []string{
	"could not write info to setgroups",
	"mount namespace requires privileges",
}

// vagueRefusal is what a refused launch with a FUSE mount prints. Other limits print it too, so it counts only while the restriction is on.
const vagueRefusal = "failed to create container process: operation not permitted"

// refusedOutput reports whether apptainer's output says a user namespace was refused.
func refusedOutput(out string) bool {
	out = strings.ToLower(out)
	for _, msg := range refusalMessages {
		if strings.Contains(out, msg) {
			return true
		}
	}
	return strings.Contains(out, vagueRefusal) && restriction() != noRestriction
}

// blocked reports whether b is refused a user namespace, by launching it on an empty directory.
//   - An apptainer that is allowed gets past the namespace and fails on the missing command.
//   - It is asked only after a launch failed, so a working launch never pays for it.
func (b Bin) blocked() bool {
	dir, err := os.MkdirTemp("", "cnt-userns-probe-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.Path, "exec", dir, "/bin/true")
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		return slices.Contains(inheritedBindVars, key)
	})
	out, _ := cmd.CombinedOutput()
	return refusedOutput(string(out))
}

// refusedLaunch reports whether a failed launch of bin was a refused user namespace.
//   - Output that names the refusal decides it.
//   - Stderr attached to a terminal leaves no output to read. A failed launch under the restriction is then settled by launching bin on an empty directory.
func refusedLaunch(bin Bin, failure *ApptainerError) bool {
	if refusedOutput(failure.Output) {
		return true
	}
	return failure.ExitCode() > 0 && restriction() != noRestriction && bin.blocked()
}

// blockedError is the error for a launch of bin the system refused a user namespace for.
//   - It names the common cause when a sysctl shows one, and says what an administrator does.
//   - When bin is the toolchain's and another apptainer on PATH is known to work, it names the command that selects it.
func blockedError(bin Bin) error {
	var admin string
	switch restriction() {
	case appArmorRestriction:
		admin = "add it to the AppArmor allowlist, or disable the restriction with kernel.apparmor_restrict_unprivileged_userns=0"
	case noNamespacesLeft:
		admin = "raise user.max_user_namespaces above 0"
	case cloneDisabled:
		admin = "set kernel.unprivileged_userns_clone=1"
	default:
		admin = "allow unprivileged user namespaces"
	}
	if alt := bin.workingAlternative(); alt != "" {
		return fmt.Errorf("%w for %s.\n  Use the host apptainer instead: condatainer config set host_apptainer %s\n  Or an administrator can %s",
			ErrUsernsBlocked, bin.Path, alt, admin)
	}
	return fmt.Errorf("%w for %s.\n  An administrator can %s", ErrUsernsBlocked, bin.Path, admin)
}

// workingAlternative returns the apptainer on PATH that can start a container here, or "".
//   - Only a refused toolchain apptainer has one: a refused host_apptainer was the user's own choice.
//   - It is a suggestion. An apptainer on PATH is never used without host_apptainer.
//   - It must be apptainer 1.4 or newer, and run a command in a sandbox with the host's system directories.
func (b Bin) workingAlternative() string {
	if restriction() == noRestriction {
		return ""
	}
	if installed, ok := libexec.ApptainerPath(); !ok || installed != b.Path {
		return ""
	}
	path := otherApptainerOnPath(b.Path)
	if path == "" {
		utils.PrintDebug("No other apptainer on PATH to suggest")
		return ""
	}
	candidate := Bin{Path: path}
	if err := candidate.requireZstd("exec"); err != nil {
		utils.PrintDebug("Not suggesting %s: %v", path, err)
		return ""
	}
	if out, ok := candidate.runsCommand(); !ok {
		utils.PrintDebug("Not suggesting %s: it did not run /bin/true: %s", path, out)
		return ""
	}
	return candidate.Path
}

// otherApptainerOnPath returns the first apptainer on PATH that is not the file at self, or "".
func otherApptainerOnPath(self string) string {
	selfInfo, selfErr := os.Stat(self)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "apptainer")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		if selfErr == nil && os.SameFile(selfInfo, info) {
			continue
		}
		return path
	}
	return ""
}

// runsCommand reports whether b starts a container and runs /bin/true in it, with the host's system directories bound in.
// It returns the launch's output, for the reason it did not.
func (b Bin) runsCommand() (string, bool) {
	dir, err := os.MkdirTemp("", "cnt-userns-probe-")
	if err != nil {
		return err.Error(), false
	}
	defer os.RemoveAll(dir)

	args := []string{"exec", "--no-home", "--pwd", "/"}
	for _, name := range []string{"usr", "bin", "lib", "lib64"} {
		real, err := filepath.EvalSymlinks("/" + name)
		if err != nil {
			continue
		}
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			return err.Error(), false
		}
		args = append(args, "--bind", real+":/"+name)
	}
	args = append(args, dir, "/bin/true")

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		return slices.Contains(inheritedBindVars, key)
	})
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err == nil
}
