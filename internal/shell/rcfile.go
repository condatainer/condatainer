package shell

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// Action is what a write did to a file.
type Action string

const (
	Created   Action = "created"
	Updated   Action = "updated"
	Appended  Action = "appended"
	Unchanged Action = "unchanged"
	Removed   Action = "removed"
	Absent    Action = "absent"
)

// Change is the outcome for one file.
type Change struct {
	Path     string
	Action   Action
	Warnings []string
}

// maxLinks bounds the symlinks followed to the file that holds the content.
const maxLinks = 40

// realPath follows path through any symlinks, including one whose target does not exist yet.
func realPath(path string) (string, error) {
	for range maxLinks {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return path, nil
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", fmt.Errorf("%w: too many symbolic links at %s", ErrNotWritable, path)
}

// span is a complete block: the line indices of its start and end markers.
type span struct{ start, end int }

// scan finds the complete blocks of lines and the marker lines that belong to none.
//   - A block is a start marker followed by an end marker.
//   - A second start marker before the end discards the first.
func scan(lines []string) (blocks []span, strayStarts, strayEnds []int) {
	pending := -1
	for i, line := range lines {
		switch strings.TrimRight(line, " \t\r") {
		case StartMarker:
			if pending >= 0 {
				strayStarts = append(strayStarts, pending)
			}
			pending = i
		case EndMarker:
			if pending < 0 {
				strayEnds = append(strayEnds, i)
				continue
			}
			blocks = append(blocks, span{pending, i})
			pending = -1
		}
	}
	if pending >= 0 {
		strayStarts = append(strayStarts, pending)
	}
	return blocks, strayStarts, strayEnds
}

func strayWarnings(path string, starts, ends []int) []string {
	var out []string
	for _, i := range starts {
		out = append(out, fmt.Sprintf("%s:%d: start marker without an end marker; left as it is", path, i+1))
	}
	for _, i := range ends {
		out = append(out, fmt.Sprintf("%s:%d: end marker without a start marker; left as it is", path, i+1))
	}
	return out
}

// readLines returns the lines of the file at real, or nil when it does not exist.
func readLines(real string) (lines []string, existed bool, err error) {
	data, err := os.ReadFile(real)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%w: %s: %w", ErrNotWritable, real, err)
	}
	return strings.Split(string(data), "\n"), true, nil
}

// ApplyBlock puts block, a marked block, into the rc file at path.
//   - The first complete block is replaced where it is. Other complete blocks are left and named in a warning.
//   - With no complete block, the block is appended. A lone marker line is left and named in a warning.
//   - A file that is a symlink stays one: the file it points to is rewritten.
//   - A write that would change nothing is skipped.
func ApplyBlock(path, block string) (Change, error) {
	real, err := realPath(path)
	if err != nil {
		return Change{}, err
	}
	lines, existed, err := readLines(real)
	if err != nil {
		return Change{}, err
	}
	blocks, strayStarts, strayEnds := scan(lines)
	change := Change{Path: path, Warnings: strayWarnings(real, strayStarts, strayEnds)}
	blockLines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")

	var out []string
	switch {
	case len(blocks) > 0:
		first := blocks[0]
		out = append(out, lines[:first.start]...)
		out = append(out, blockLines...)
		out = append(out, lines[first.end+1:]...)
		change.Action = Updated
		for _, extra := range blocks[1:] {
			change.Warnings = append(change.Warnings,
				fmt.Sprintf("%s:%d: another block; left as it is", real, extra.start+1))
		}
	default:
		out = appendBlock(lines, blockLines)
		change.Action = Appended
		if !existed {
			change.Action = Created
		}
	}
	text := strings.Join(out, "\n")
	if existed && text == strings.Join(lines, "\n") {
		change.Action = Unchanged
		return change, nil
	}
	return change, writeFile(real, text)
}

// appendBlock adds blockLines after lines, separated by one blank line and ending in a newline.
func appendBlock(lines, blockLines []string) []string {
	out := append([]string(nil), lines...)
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
		out = append(out, "")
	}
	out = append(out, blockLines...)
	return append(out, "")
}

// RemoveBlock takes every complete block out of the rc file at path.
//   - The blank line before a block goes with it.
//   - A lone marker line is left and named in a warning.
//   - A missing file, or one with no block, is Absent.
func RemoveBlock(path string) (Change, error) {
	real, err := realPath(path)
	if err != nil {
		return Change{}, err
	}
	lines, existed, err := readLines(real)
	if err != nil {
		return Change{}, err
	}
	blocks, strayStarts, strayEnds := scan(lines)
	change := Change{Path: path, Action: Absent, Warnings: strayWarnings(real, strayStarts, strayEnds)}
	if !existed || len(blocks) == 0 {
		return change, nil
	}
	drop := make([]bool, len(lines))
	for _, b := range blocks {
		for i := b.start; i <= b.end; i++ {
			drop[i] = true
		}
		if b.start > 0 && strings.TrimSpace(lines[b.start-1]) == "" {
			drop[b.start-1] = true
		}
	}
	var out []string
	for i, line := range lines {
		if !drop[i] {
			out = append(out, line)
		}
	}
	change.Action = Removed
	return change, writeFile(real, strings.Join(out, "\n"))
}

// ApplyFile writes content as the whole file at path, through a symlink if path is one.
func ApplyFile(path, content string) (Change, error) {
	real, err := realPath(path)
	if err != nil {
		return Change{}, err
	}
	change := Change{Path: path, Action: Created}
	if old, err := os.ReadFile(real); err == nil {
		if string(old) == content {
			change.Action = Unchanged
			return change, nil
		}
		change.Action = Updated
	}
	return change, writeFile(real, content)
}

// RemoveFile deletes the file at path. When path is a symlink, the link goes and its target stays.
func RemoveFile(path string) (Change, error) {
	err := os.Remove(path)
	switch {
	case err == nil:
		return Change{Path: path, Action: Removed}, nil
	case errors.Is(err, fs.ErrNotExist):
		return Change{Path: path, Action: Absent}, nil
	}
	return Change{}, fmt.Errorf("%w: %s: %w", ErrNotWritable, path, err)
}

// writeFile replaces the file at real with text.
//   - An existing file is rewritten through a temporary file beside it, with its permissions.
//   - A new file is created, and its directory when missing.
func writeFile(real, text string) error {
	dir := filepath.Dir(real)
	if err := utils.MkdirAllShared(dir); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotWritable, real, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		f, err := utils.CreateFileWritable(real)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrNotWritable, real, err)
		}
		_, werr := f.WriteString(text)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("%w: %s: %w", ErrNotWritable, real, werr)
		}
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".condatainer-*")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotWritable, real, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	_, werr := tmp.WriteString(text)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), info.Mode().Perm())
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), real)
	}
	if werr != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotWritable, real, werr)
	}
	return nil
}
