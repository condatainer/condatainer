package shell

import (
	"errors"
	"strings"
	"testing"
)

func TestBlockPerShell(t *testing.T) {
	cases := []struct {
		shell Shell
		want  []string
		not   []string
	}{
		{Bash, []string{StartMarker, EndMarker, "export PATH=\"$__cnt_bin:$PATH\"",
			"source <(condatainer shell completion bash --alias cnt)", "source <(condatainer env hook --shell bash)"}, nil},
		{Zsh, []string{StartMarker, "source <(condatainer shell completion zsh --alias cnt)", "source <(condatainer env hook --shell zsh)"}, nil},
		{Fish, []string{"fish_add_path -g '/opt/cnt/bin'", "condatainer shell completion fish --alias cnt | source",
			"condatainer env hook --shell fish | source"}, []string{StartMarker, EndMarker}},
	}
	for _, c := range cases {
		got := Setup{Shell: c.shell, Alias: "cnt", BinDir: "/opt/cnt/bin"}.Block()
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s block lacks %q:\n%s", c.shell, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s block has %q:\n%s", c.shell, n, got)
			}
		}
	}
}

func TestBlockWithoutBinDirOrAlias(t *testing.T) {
	got := Setup{Shell: Bash}.Block()
	if strings.Contains(got, "PATH") || strings.Contains(got, "--alias") {
		t.Errorf("block has a PATH line or alias nobody asked for:\n%s", got)
	}
}

func TestBlockQuotesAPathWithASpaceAndQuote(t *testing.T) {
	got := Setup{Shell: Bash, BinDir: "/opt/it's here/bin"}.Block()
	if !strings.Contains(got, `__cnt_bin='/opt/it'\''s here/bin'`) {
		t.Errorf("path is not quoted:\n%s", got)
	}
}

func TestParseAndAlias(t *testing.T) {
	if _, err := Parse("tcsh"); !errors.Is(err, ErrUnsupportedShell) {
		t.Errorf("Parse(tcsh) = %v, want ErrUnsupportedShell", err)
	}
	if ValidAlias("a b") || ValidAlias("a;b") || !ValidAlias("cnt-2_x") {
		t.Error("ValidAlias accepts a word with a space or ';', or refuses a plain word")
	}
}

func TestRCFile(t *testing.T) {
	if got := RCFile(Zsh, "/h", "/z"); got != "/z/.zshrc" {
		t.Errorf("zsh with ZDOTDIR = %q", got)
	}
	if got := RCFile(Zsh, "/h", ""); got != "/h/.zshrc" {
		t.Errorf("zsh = %q", got)
	}
	if got := RCFile(Fish, "/h", "/z"); got != "/h/.config/fish/conf.d/condatainer.fish" {
		t.Errorf("fish = %q", got)
	}
	if got := RCFile(Bash, "/h", "/z"); got != "/h/.bashrc" {
		t.Errorf("bash = %q", got)
	}
}
