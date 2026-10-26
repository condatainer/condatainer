package lock

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// IgnoreLine turns a command-line path into the line the lock ignore list stores.
//   - A relative path is taken from cwd, and a leading `/` is the project root. An absolute path inside the project is converted to its root-relative form.
//   - The line is anchored at the root with a leading `/`, and a trailing `/` is kept.
//   - A path that climbs out of the project, or names the whole project, is an error.
func IgnoreLine(root, cwd, arg string) (string, error) {
	dirOnly := strings.HasSuffix(arg, "/")
	var rel string
	switch {
	case filepath.IsAbs(arg) && !within(root, arg):
		rel = strings.TrimPrefix(path.Clean(filepath.ToSlash(arg)), "/")
		if rel == "" {
			rel = "."
		}
	case filepath.IsAbs(arg):
		rel = mustRel(root, arg)
	default:
		rel = mustRel(root, filepath.Join(cwd, arg))
	}
	if rel == "." {
		return "", fmt.Errorf("%s is the whole project", arg)
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%s is outside the project", arg)
	}
	line := "/" + rel
	if dirOnly {
		line += "/"
	}
	return line, nil
}

func within(root, file string) bool {
	rel, err := filepath.Rel(root, file)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func mustRel(root, file string) string {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return ".."
	}
	return filepath.ToSlash(rel)
}

// ReadIgnoreList returns the non-blank lines of the lock ignore list, comments included, in file order. A missing file is an empty list.
func ReadIgnoreList(root string) ([]string, error) {
	text, err := os.ReadFile(IgnorePath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(string(text), "\n") {
		if line = strings.TrimRight(line, "\r"); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// WriteIgnoreList stores the lines sorted and without repeats, and removes the file when none are left.
//   - The list is committed with the lock, so the order is fixed to keep a diff to the lines that changed.
//   - The write goes to a sibling file and is renamed into place.
func WriteIgnoreList(root string, lines []string) error {
	lines = append([]string(nil), lines...)
	sort.Strings(lines)
	var out strings.Builder
	for i, line := range lines {
		if i == 0 || line != lines[i-1] {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	if out.Len() == 0 {
		if err := os.Remove(IgnorePath(root)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := utils.MkdirAllShared(Dir(root)); err != nil {
		return err
	}
	staged := filepath.Join(Dir(root), stagingName("ignore"))
	defer os.Remove(staged) //nolint:errcheck
	temp, err := utils.CreateFileWritable(staged)
	if err != nil {
		return err
	}
	if _, err := temp.WriteString(out.String()); err != nil {
		temp.Close() //nolint:errcheck
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(staged, IgnorePath(root)); err != nil {
		return err
	}
	utils.ShareWithParentGroup(IgnorePath(root))
	return nil
}

// IgnoredByGit reports whether the `.gitignore` files already ignore a path of the project, or a directory above it.
func IgnoredByGit(root, rel string) (bool, error) {
	m, err := newMembers(root, nil)
	if err != nil {
		return false, err
	}
	for _, isDir := range []bool{false, true} {
		parts := strings.Split(strings.Trim(rel, "/"), "/")
		for i := range parts {
			if m.gitIgnored(strings.Join(parts[:i+1], "/"), isDir || i < len(parts)-1) {
				return true, nil
			}
		}
	}
	return false, nil
}

// ScriptsMatching lists the project's scripts that one ignore line would leave out, whatever the lock ignore list holds now.
func ScriptsMatching(root, line string) ([]string, error) {
	m, err := newMembers(root, nil)
	if err != nil {
		return nil, err
	}
	m.extra = utils.ParseIgnore(nil)
	pattern := utils.ParseIgnore([]byte(line))
	var scripts []string
	err = walkScripts(root, m, func(_, rel string) error {
		parts := strings.Split(rel, "/")
		for i := range parts {
			if pattern.Match(strings.Join(parts[:i+1], "/"), i < len(parts)-1) == utils.IgnoreYes {
				scripts = append(scripts, rel)
				return nil
			}
		}
		return nil
	}, nil)
	return scripts, err
}
