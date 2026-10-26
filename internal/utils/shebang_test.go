package utils

import (
	"errors"
	"testing"
)

func TestShebangCommand(t *testing.T) {
	for script, want := range map[string][2]string{
		"#!/bin/bash\n":              {"/bin/bash", ""},
		"#!/bin/bash -e -x\necho\n":  {"/bin/bash", "-e -x"},
		"#!/usr/bin/env python3\r\n": {"/usr/bin/env", "python3"},
	} {
		interpreter, argument, err := ShebangCommand([]byte(script))
		if err != nil || interpreter != want[0] || argument != want[1] {
			t.Errorf("%q: %q %q %v, want %q", script, interpreter, argument, err, want)
		}
	}
	if _, _, err := ShebangCommand([]byte("#!/bin/sh\n")); !errors.Is(err, ErrShebang) {
		t.Errorf("an unsupported line: %v", err)
	}
}

func TestCheckShebang(t *testing.T) {
	for _, ok := range []string{
		"#!/bin/bash\necho hi\n", "#!/usr/bin/bash -e\n", "#!/usr/bin/env python3\n", "#!/bin/env bash\r\n",
	} {
		if err := CheckShebang([]byte(ok)); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "echo hi\n", "#!/bin/sh\n", "#!/usr/bin/python3\n", "#!/usr/bin/env\n", "#!/usr/bin/env -S bash -e\n", "#!\n",
	} {
		if err := CheckShebang([]byte(bad)); !errors.Is(err, ErrShebang) {
			t.Errorf("%q was accepted", bad)
		}
	}
}
