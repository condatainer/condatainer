package credential

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// Reach is a class of people, from the narrowest to the widest.
type Reach int

const (
	// ReachOwner is the file's owner alone.
	ReachOwner Reach = iota
	// ReachGroup is the owner and the file's group.
	ReachGroup
	// ReachEveryone is every user on the system.
	ReachEveryone
)

// Access is who can read a credential file and who can replace it, from the
// file's and its directory's mode bits. ACLs are not consulted.
type Access struct {
	Readers Reach
	Writers Reach
	Group   string // the file's group, named in a message about the group
	File    os.FileMode
	Dir     os.FileMode
}

// accessOf reads who can reach path.
//   - A class reads when the directory lets it in and the file lets it read.
//   - It replaces when the directory lets it in and either the file is writable by it or the directory is, unless the directory is sticky, where deleting another user's file is refused.
func accessOf(path string) (Access, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Access{}, err
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return Access{}, err
	}
	fm, dm := info.Mode().Perm(), dir.Mode().Perm()
	sticky := dir.Mode()&os.ModeSticky != 0

	access := Access{File: fm, Dir: dm}
	switch {
	case dm&0o001 != 0 && fm&0o004 != 0:
		access.Readers = ReachEveryone
	case dm&0o010 != 0 && fm&0o040 != 0:
		access.Readers = ReachGroup
	}
	switch {
	case dm&0o001 != 0 && (fm&0o002 != 0 || dm&0o002 != 0 && !sticky):
		access.Writers = ReachEveryone
	case dm&0o010 != 0 && (fm&0o020 != 0 || dm&0o020 != 0 && !sticky):
		access.Writers = ReachGroup
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		access.Group = strconv.FormatUint(uint64(stat.Gid), 10)
		if g, err := user.LookupGroupId(access.Group); err == nil {
			access.Group = g.Name
		}
	}
	return access, nil
}

// Finding is one thing to tell the person who saved a credential.
type Finding struct {
	Warn bool // false is a note
	Text string
}

// Findings reports what is wrong with who can reach a layer's credential file.
//   - The user layer is personal, so anyone else reading it is a warning; a shared layer is meant to be read by others, so its readers are only shown.
//   - Anyone other than the owner being able to replace the file is always reported.
func Findings(layer, path string) []Finding {
	access, err := accessOf(path)
	if err != nil {
		return nil
	}
	var out []Finding
	if layer == "user" && access.Readers != ReachOwner {
		out = append(out, Finding{Warn: true, Text: fmt.Sprintf(
			"%s is %04o, so others can read it. Run: chmod 600 %s", path, access.File, path)})
	}
	switch access.Writers {
	case ReachEveryone:
		out = append(out, Finding{Warn: true, Text: fmt.Sprintf(
			"%s is %04o in a %04o directory, so anyone can replace it. Run: chmod go-w %s %s",
			path, access.File, access.Dir, path, filepath.Dir(path))})
	case ReachGroup:
		out = append(out, Finding{Warn: layer == "user", Text: fmt.Sprintf(
			"%s is %04o in a %04o directory, so group %s can replace it", path, access.File, access.Dir, access.Group)})
	}
	return out
}

// Mode is path's permission bits and the name of its group. ok is false when
// the file cannot be inspected.
func Mode(path string) (perm os.FileMode, group string, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, "", false
	}
	access, err := accessOf(path)
	if err != nil {
		return 0, "", false
	}
	return info.Mode().Perm(), access.Group, true
}
