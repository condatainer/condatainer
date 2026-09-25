package publish

import (
	"context"
	"os/exec"
	"strings"

	"github.com/condatainer/condatainer/internal/logging"
)

// OriginURL reports the project's code repository as an https URL, derived from the checkout's `origin` remote, or "" when it cannot say.
//   - Only origin: which of several remotes is "the" project is not a tool's silent choice.
//   - Empty is an ordinary answer, not an error: the annotation is omitted and --source supplies one.
func OriginURL(ctx context.Context, root string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		logging.FromContext(ctx).Debug("no git origin to derive the project source from", "root", root, "err", err)
		return ""
	}
	return NormalizeRemoteURL(string(out))
}

// NormalizeRemoteURL folds a git remote into the one https URL an annotation
// should carry, or "" for a remote naming no browsable repository.
//
//   - It accepts https, ssh:// and scp-style forms, and trims `.git` and a trailing slash.
//   - SSH and HTTPS clones leave different spellings, and GHCR links a package only on an exact https match.
//   - An absolute path or a single component names no owner, so it returns "".
func NormalizeRemoteURL(raw string) string {
	url := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(url, "https://"):
		url = strings.TrimPrefix(url, "https://")
	case strings.HasPrefix(url, "http://"):
		url = strings.TrimPrefix(url, "http://")
	case strings.HasPrefix(url, "ssh://git@"):
		url = strings.TrimPrefix(url, "ssh://git@")
	case strings.HasPrefix(url, "git@"):
		// scp-style: the colon separates host from path and is not a port.
		url = strings.Replace(strings.TrimPrefix(url, "git@"), ":", "/", 1)
	default:
		return ""
	}
	url = strings.TrimSuffix(strings.Trim(url, "/"), ".git")

	host, repoPath, found := strings.Cut(url, "/")
	if !found || host == "" || repoPath == "" || strings.HasPrefix(repoPath, "/") {
		return ""
	}
	// At least owner/repo. Nested groups make a deeper path a repository on most
	// hosts; on github.com it is a page inside one, which GHCR links to nothing.
	segments := strings.Split(repoPath, "/")
	if len(segments) < 2 || (host == "github.com" && len(segments) != 2) {
		return ""
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}
	return "https://" + host + "/" + repoPath
}
