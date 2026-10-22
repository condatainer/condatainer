package conda

import (
	"os/exec"
	"strings"
	"testing"
)

func TestMMHookEmptyOutsideAMountedEnvironment(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "")
	t.Setenv("CNT_CONDA_ROOT", "")
	if got := MMHook(ShellBash); got != "" {
		t.Errorf("MMHook = %q, want empty", got)
	}
	t.Setenv("IN_CONDATAINER", "1")
	if got := MMHook(ShellBash); got != "" {
		t.Errorf("MMHook without CNT_CONDA_ROOT = %q, want empty", got)
	}
}

func TestMMHookHasTheFunctionAndItsCompletion(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "1")
	t.Setenv("CNT_CONDA_ROOT", "/cnt_env")
	want := map[Shell][]string{
		ShellBash: {"mm() {", "reactivate --shell bash", "complete -o default -F _mm_complete mm"},
		ShellZsh:  {"mm() {", "reactivate --shell zsh", "compdef _mm mm"},
		ShellFish: {"function mm", "reactivate --shell fish | source", "complete -c mm --wraps 'condatainer env'"},
	}
	for shell, parts := range want {
		got := MMHook(shell)
		for _, part := range parts {
			if !strings.Contains(got, part) {
				t.Errorf("%s hook lacks %q:\n%s", shell, part, got)
			}
		}
	}
}

// The text is parsed by the shell itself, where one is installed.
func TestMMHookParsesInEachShell(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "1")
	t.Setenv("CNT_CONDA_ROOT", "/cnt_env")
	checks := map[Shell][]string{
		ShellBash: {"bash", "-n"},
		ShellZsh:  {"zsh", "-n"},
		ShellFish: {"fish", "--no-execute"},
	}
	for shell, argv := range checks {
		bin, err := exec.LookPath(argv[0])
		if err != nil {
			t.Logf("%s is not installed", argv[0])
			continue
		}
		cmd := exec.Command(bin, argv[1:]...)
		cmd.Stdin = strings.NewReader(MMHook(shell))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s rejects its hook: %v\n%s", shell, err, out)
		}
	}
}
