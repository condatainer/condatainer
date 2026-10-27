package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A #BIND: value reaches the wrapper's shell with only $NAME and ${NAME} left to expand:
// a space, a quote or a command substitution in it is text.
func TestHelperBindsExpandOnlyVariables(t *testing.T) {
	script := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(script, []byte("#!/bin/bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, err := buildCondatainerCmd(RunOptions{
		ScriptPath: script,
		ExtraBinds: []string{`$HOME/my data:/d"ata`, "$(touch /tmp/x):/y", "${CNT_HELPER_STATE_DIR}/logs:/logs"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`--bind "${HOME}"'/my data:/d"ata'`,
		`--bind '$(touch /tmp/x):/y'`,
		`--bind "${CNT_HELPER_STATE_DIR}"'/logs:/logs'`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %s:\n%s", want, cmd)
		}
	}
}

func TestShellQuoteVars(t *testing.T) {
	for in, want := range map[string]string{
		"/plain:/p":  `'/plain:/p'`,
		"$A:/x":      `"${A}"':/x'`,
		"${A}${B_1}": `"${A}""${B_1}"`,
		"$":          `'$'`,
		"$1:${:$(x)": `'$1:${:$(x)'`,
		"it's $A":    `'it'\''s '"${A}"`,
		"":           `''`,
	} {
		if got := shellQuoteVars(in); got != want {
			t.Errorf("shellQuoteVars(%q) = %s, want %s", in, got, want)
		}
	}
}
