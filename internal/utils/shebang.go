package utils

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// ErrShebang reports a script whose first line is not an allowed interpreter line.
var ErrShebang = errors.New("script: unsupported interpreter line")

// ShebangCommand returns the interpreter and the argument a script's first line names, as the kernel reads it.
//   - The argument is the rest of the line as one string, and "" when there is none.
//   - A script whose first line CheckShebang refuses is an error.
func ShebangCommand(script []byte) (interpreter, argument string, err error) {
	if err := CheckShebang(script); err != nil {
		return "", "", err
	}
	first, _, _ := bytes.Cut(script, []byte("\n"))
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(first)), "#!"))
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		return rest[:i], strings.TrimSpace(rest[i:]), nil
	}
	return rest, "", nil
}

// CheckShebang reports whether a script's first line names an interpreter that every container has.
//   - Allowed: #!/bin/bash and #!/usr/bin/bash, with any arguments, and #!/usr/bin/env or #!/bin/env followed by an interpreter.
//   - The word after env must not be a flag, so `env -S` is refused.
func CheckShebang(script []byte) error {
	first, _, _ := bytes.Cut(script, []byte("\n"))
	line := strings.TrimSpace(string(first))
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if !strings.HasPrefix(line, "#!") || len(fields) == 0 {
		return fmt.Errorf("%w: the first line must start with #!", ErrShebang)
	}
	switch fields[0] {
	case "/bin/bash", "/usr/bin/bash":
		return nil
	case "/usr/bin/env", "/bin/env":
		if len(fields) > 1 && !strings.HasPrefix(fields[1], "-") {
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not #!/bin/bash, #!/usr/bin/bash, #!/usr/bin/env <interpreter> or #!/bin/env <interpreter>", ErrShebang, line)
}
