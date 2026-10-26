package helper

import (
	"strings"
	"testing"
)

// A #BIND: value reaches the wrapper's shell as one single-quoted word: a space, a quote
// or a command substitution in it is text, and `exec` expands the variables later.
func TestHelperBindsAreSingleQuoted(t *testing.T) {
	cmd, err := buildCondatainerCmd(RunOptions{
		ScriptPath: "/h/script",
		ExtraBinds: []string{`$HOME/my data:/d"ata`, "$(touch /tmp/x):/y", "${CNT_HELPER_STATE_DIR}/logs:/logs"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`--bind '$HOME/my data:/d"ata'`,
		`--bind '$(touch /tmp/x):/y'`,
		`--bind '${CNT_HELPER_STATE_DIR}/logs:/logs'`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %s:\n%s", want, cmd)
		}
	}
}
