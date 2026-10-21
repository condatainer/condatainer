package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/registry"
)

// ErrShallowHistory reports a checkout whose history is cut short, so the locks of earlier commits cannot all be read.
var ErrShallowHistory = errors.New("this checkout has a shallow history")

// Pinned lists the manifest digests the lock records in repository.
func Pinned(l *lock.Lock, repository string) map[string]bool {
	pinned := map[string]bool{}
	want := registry.TrimBaseScheme(repository)
	for _, remotes := range l.Remotes {
		for _, r := range remotes {
			if registry.TrimBaseScheme(r.Repository) == want {
				pinned[r.ManifestDigest] = true
			}
		}
	}
	return pinned
}

// HistoricPinned adds what every earlier version of the lock records in repository.
//   - It reads the lock at each commit of HEAD's history that changed it. Another branch's locks are not read.
//   - A shallow checkout is refused, since the commits it lacks may have pinned more.
func HistoricPinned(ctx context.Context, root, repository string, pinned map[string]bool) error {
	out, err := git(ctx, root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return fmt.Errorf("cannot read the git history of %s: %w", root, err)
	}
	if strings.TrimSpace(out) == "true" {
		return fmt.Errorf("%w, so --git-history could remove what an older commit pins; fetch the full history first", ErrShallowHistory)
	}
	file := path.Join(lock.DirName, lock.FileName)
	commits, err := git(ctx, root, "log", "--format=%H", "--", file)
	if err != nil {
		return fmt.Errorf("cannot list the commits that changed the lock: %w", err)
	}
	for _, commit := range strings.Fields(commits) {
		data, err := git(ctx, root, "show", commit+":./"+file)
		if err != nil {
			// A commit that deleted the lock has none to read.
			if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
				continue
			}
			return fmt.Errorf("cannot read the lock at commit %s: %w", commit[:min(12, len(commit))], err)
		}
		l, err := lock.Unmarshal([]byte(data))
		if err != nil {
			return fmt.Errorf("the lock at commit %s cannot be read: %w", commit[:min(12, len(commit))], err)
		}
		for digest := range Pinned(l, repository) {
			pinned[digest] = true
		}
	}
	return nil
}

// git runs one read-only git command in the project, and returns its output.
func git(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
