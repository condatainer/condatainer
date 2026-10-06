// Package toolpath finds a runnable path for an external tool by name — the
// one place that decision is made. internal/libexec's self-provisioned
// toolchain is checked first and required over whatever the host has, when
// the tool is installed there; nothing else in this package or its caller
// decides that preference. Otherwise the search is PATH, then the directory of
// tools bundled with the host apptainer, then the FHS fallback directories. A
// host mksquashfs or unsquashfs below the version floor is skipped.
package toolpath

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/condatainer/condatainer/internal/libexec"
	"github.com/condatainer/condatainer/internal/logging"
)

// ErrToolMissing reports that name could not be found anywhere Resolve looks.
var ErrToolMissing = errors.New("required tool not available")

// fhsFallbackDirs is Apptainer's own BinaryPath default search order for
// this class of external tool (apptainer's pkg/util/apptainerconf,
// unexported there): PATH, then the standard FHS directories. Matching it
// exactly means a tool findable by Apptainer's own mount is findable by
// CondaTainer's direct freeze/unfreeze path too, rather than a shorter,
// invented list.
var fhsFallbackDirs = []string{
	"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin",
}

// Resolve finds name for a host-side invocation.
//   - Order: libexec's copy, the caller's PATH, the tools bundled with the host apptainer, then the FHS directories directly.
//   - libexec's copy is required, not merely first, so it wins even over a same-named binary on PATH.
//   - A host binary below its version floor is skipped, and the reason is kept for the error.
//   - If none has name it returns ErrToolMissing, wrapping libexec.NotProvisionedMessage(name).
//   - That message names the `condatainer update --libexec` fix only when libexec installs name.
//   - A container-bound call needs the containing directory bound in too. It uses libexec.ApptainerPath, MicromambaPath or Path instead.
func Resolve(name string) (string, error) {
	if p, ok := libexec.Path(name); ok && isExecutable(p) {
		return p, nil
	}
	var skipped []string
	usable := func(p string) bool {
		if reason := floorProblem(name, p); reason != "" {
			skipped = append(skipped, p+": "+reason)
			return false
		}
		return true
	}
	seen := map[string]bool{}
	search := func(dirs []string) (string, bool) {
		for _, dir := range dirs {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			p := filepath.Join(dir, name)
			if isExecutable(p) && usable(p) {
				return p, true
			}
		}
		return "", false
	}
	if p, ok := search(filepath.SplitList(os.Getenv("PATH"))); ok {
		return p, nil
	}
	// Asked for only when PATH missed: it runs the host apptainer.
	if p, ok := search(bundledDirs()); ok {
		return p, nil
	}
	if p, ok := search(fhsFallbackDirs); ok {
		return p, nil
	}
	msg := NotFoundMessage(name)
	if len(skipped) > 0 {
		msg += " (skipped " + strings.Join(skipped, "; ") + ")"
	}
	return "", fmt.Errorf("%w: %s", ErrToolMissing, msg)
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

// floors caches, per path, why a binary failed its version floor ("" = fine),
// so each host binary is run for its version once per process.
var floors sync.Map

func floorProblem(name, path string) string {
	if cached, ok := floors.Load(path); ok {
		return cached.(string)
	}
	kind := "floor:" + name
	reason, ok := cacheGet(kind, path)
	if !ok {
		if err := libexec.CheckFloor(context.Background(), path, name); err != nil {
			reason = err.Error()
		}
		cachePut(kind, path, reason)
	}
	floors.Store(path, reason)
	return reason
}

// bundled caches, per apptainer binary, the directory of tools it ships.
var bundled sync.Map

// bundledDirs returns the directory of tools bundled with the host apptainer
// (mksquashfs, unsquashfs, squashfuse_ll, fuse2fs, ...), or nothing when no
// host apptainer is configured or it will not say where they are.
func bundledDirs() []string {
	bin := SystemApptainer()
	if bin == "" {
		bin = "apptainer"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil
	}
	if cached, ok := bundled.Load(path); ok {
		if dir := cached.(string); dir != "" {
			return []string{dir}
		}
		return nil
	}
	dir, ok := cacheGet("bundled", path)
	if !ok {
		var out bytes.Buffer
		cmd := exec.Command(path, "buildcfg")
		cmd.Stdout = &out
		if cmd.Run() == nil {
			for _, line := range strings.Split(out.String(), "\n") {
				if v, ok := strings.CutPrefix(line, "LIBEXECDIR="); ok {
					dir = filepath.Join(strings.TrimSpace(v), filepath.Base(path), "bin")
				}
			}
		}
		cachePut("bundled", path, dir)
	}
	bundled.Store(path, dir)
	if dir != "" {
		return []string{dir}
	}
	return nil
}

// NotFoundMessage explains why name could not be resolved, using libexec's wording.
//   - It is re-exported so a caller that already resolves here need not import internal/libexec for a message.
//   - Naming the `condatainer update --libexec` fix is libexec's call, since it depends on whether libexec installs name.
func NotFoundMessage(name string) string {
	return libexec.NotProvisionedMessage(name)
}

// Command resolves name (see Resolve) and returns a context-bound *exec.Cmd
// ready to configure and run, with the invocation logged at debug level.
// The caller wires stdout/stderr and wraps a failure in whatever error
// shape its own domain needs — this package has no opinion on that.
func Command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	resolved, err := Resolve(name)
	if err != nil {
		return nil, err
	}
	logging.FromContext(ctx).Debug("running "+name, "args", strings.Join(args, " "))
	return exec.CommandContext(ctx, resolved, args...), nil
}
