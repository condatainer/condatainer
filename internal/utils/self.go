package utils

import (
	"os"
	"regexp"
	"strings"
)

// plainWord matches a word that needs no quoting in a shell command.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:=+@%-]+$`)

// executable locates the running binary. Tests replace it.
var executable = os.Executable

// SelfPath is the path of the running condatainer, or the bare name when it
// cannot be read.
func SelfPath() string {
	exe, err := executable()
	if err != nil || exe == "" {
		return "condatainer"
	}
	return exe
}

// SelfCommand is SelfPath quoted for a shell: the word a job script or a
// remote command starts with, so it runs the binary that wrote it.
func SelfCommand() string {
	exe := SelfPath()
	if plainWord.MatchString(exe) {
		return exe
	}
	return ShellQuote(exe)
}

// ShellQuote wraps s in single quotes for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
