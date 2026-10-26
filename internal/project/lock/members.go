package lock

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// IgnoreFileName is the lock ignore list inside the lock directory.
const IgnoreFileName = "ignore"

// ErrIgnoreNegation reports a `!` line in the lock ignore list.
var ErrIgnoreNegation = errors.New("a `!` line is not allowed in the lock ignore list")

// IgnorePath is the lock ignore list of a project.
func IgnorePath(root string) string { return filepath.Join(Dir(root), IgnoreFileName) }

// members decides which files of a project are scripts the lock reads.
//   - A dot-directory, a lock directory and a directory named in the extra list are skipped.
//   - A path is skipped when the `.gitignore` of its directory or of a parent ignores it. A deeper file wins over a shallower one.
//   - The lock ignore list is applied last. It has no `!` lines.
type members struct {
	root  string
	skip  map[string]bool
	git   map[string]*utils.Ignore
	extra *utils.Ignore
}

func newMembers(root string, excludeDirs []string) (*members, error) {
	m := &members{root: root, skip: map[string]bool{DirName: true}, git: map[string]*utils.Ignore{}}
	for _, dir := range excludeDirs {
		if dir = strings.TrimSpace(dir); dir != "" {
			m.skip[dir] = true
		}
	}
	text, err := os.ReadFile(IgnorePath(root))
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.extra = utils.ParseIgnore(nil)
	case err != nil:
		return nil, err
	default:
		m.extra = utils.ParseIgnore(text)
		if lines := m.extra.Negations(); len(lines) > 0 {
			return nil, fmt.Errorf("%s line %d: %w: a file `.gitignore` ignores is not part of the project, so edit `.gitignore` to bring it back",
				IgnorePath(root), lines[0], ErrIgnoreNegation)
		}
	}
	return m, nil
}

// ignores reports whether a slash-separated path under the root is left out. A directory that is left out hides everything inside it.
func (m *members) ignores(rel string, isDir bool) bool {
	if isDir && (m.skip[path.Base(rel)] || strings.HasPrefix(path.Base(rel), ".")) {
		return true
	}
	return m.gitIgnored(rel, isDir) || m.extra.Match(rel, isDir) == utils.IgnoreYes
}

// gitIgnored applies the `.gitignore` of the path's directory and of each parent; the deepest file that has a verdict decides.
func (m *members) gitIgnored(rel string, isDir bool) bool {
	for dir := path.Dir(rel); ; dir = path.Dir(dir) {
		if dir == "." {
			dir = ""
		}
		sub := strings.TrimPrefix(strings.TrimPrefix(rel, dir), "/")
		if verdict := m.gitignore(dir).Match(sub, isDir); verdict != utils.IgnoreNone {
			return verdict == utils.IgnoreYes
		}
		if dir == "" {
			return false
		}
	}
}

// gitignore returns the rules of one directory's `.gitignore`, read once.
func (m *members) gitignore(dir string) *utils.Ignore {
	if ig, ok := m.git[dir]; ok {
		return ig
	}
	text, _ := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(dir), ".gitignore"))
	ig := utils.ParseIgnore(text)
	m.git[dir] = ig
	return ig
}

// IsMember reports whether a script is part of the project, so its declarations are pinned.
//   - It applies the rules `Scan` walks with, to the script and each directory above it.
//   - The script must be inside root.
func IsMember(root, script string) (bool, error) {
	rel, err := scriptRel(root, script)
	if err != nil {
		return false, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return false, err
	}
	m, err := newMembers(root, nil)
	if err != nil {
		return false, err
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		if m.ignores(strings.Join(parts[:i+1], "/"), i < len(parts)-1) {
			return false, nil
		}
	}
	return true, nil
}

// scriptRel returns the slash-separated path of script under root.
func scriptRel(root, script string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	script, err = filepath.Abs(script)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, script)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is not inside %s", script, root)
	}
	return filepath.ToSlash(rel), nil
}
