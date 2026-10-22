package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/shell"
	"github.com/condatainer/condatainer/internal/utils"
)

var (
	shellName  string
	shellAlias string
)

var shellCmd = &cobra.Command{
	Use:          "shell",
	Short:        "Set up condatainer in the shell",
	Long:         "Set up condatainer in the shell: PATH, tab completion, and the mm command for Conda environments.",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var shellInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Write condatainer's setup into the shell's rc file",
	Long: `Write condatainer's setup into the shell's rc file.

- It puts condatainer on PATH, loads tab completion, and defines mm inside a container.
- With home_override set, the rc file under the replacement home is set up too.
- It is refused inside a container. Run it on the host.`,
	Example: `  condatainer shell init
  condatainer shell init --shell zsh --alias cnt`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runShellInit,
}

var shellRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove condatainer's setup from the shell's rc file",
	Long: `Remove condatainer's setup from the shell's rc file.

- With home_override set, the rc file under the replacement home is cleaned too.
- It is refused inside a container. Run it on the host.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runShellRemove,
}

func init() {
	rootCmd.AddCommand(shellCmd)
	shellCmd.AddCommand(shellInitCmd, shellRemoveCmd, newCompletionCmd(false))
	rootCmd.AddCommand(newCompletionCmd(true))

	shellInitCmd.Flags().StringVar(&shellName, "shell", "",
		"bash, zsh, or fish; default: the current shell. Bash is always set up too")
	shellRemoveCmd.Flags().StringVar(&shellName, "shell", "",
		"bash, zsh, or fish; default: all of them")
	shellInitCmd.Flags().StringVar(&shellAlias, "alias", "", "Define a short alias (e.g. cnt) and complete it too")
}

// setupShell resolves --shell, else the shell running the command.
func setupShell() (shell.Shell, error) {
	name := shellName
	if name == "" {
		detected, err := detectCompletionShell()
		if err != nil {
			return "", fmt.Errorf("%w; specify it with --shell bash|zsh|fish", err)
		}
		name = detected
	}
	return shell.Parse(name)
}

// loadHomeOverride reads the config layers, which hold home_override.
func loadHomeOverride() {
	if exe, err := os.Executable(); err == nil {
		config.LoadDefaults(exe)
	}
	if err := config.LoadLayers(); err != nil {
		utils.PrintDebug("Error reading config file: %v", err)
	}
}

func runShellInit(cmd *cobra.Command, _ []string) error {
	sh, err := setupShell()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find the condatainer binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	loadHomeOverride()
	res, err := shell.Init(shell.Options{Shell: sh, Alias: shellAlias, Exe: exe})
	printShellResult(res)
	return err
}

func runShellRemove(cmd *cobra.Command, _ []string) error {
	all := shellName == ""
	shells := shell.All
	if !all {
		sh, err := shell.Parse(shellName)
		if err != nil {
			return err
		}
		shells = []shell.Shell{sh}
	}
	loadHomeOverride()
	res, err := shell.Remove(shells)
	if all {
		res = withoutAbsent(res)
		if err == nil && len(res.Changes) == 0 {
			utils.PrintMessage("No setup found in any shell's rc file")
		}
	}
	printShellResult(res)
	return err
}

// withoutAbsent drops the files that held no setup, so removing from every shell lists only what changed.
func withoutAbsent(res shell.Result) shell.Result {
	kept := res.Changes[:0:0]
	for _, c := range res.Changes {
		if c.Action != shell.Absent {
			kept = append(kept, c)
		}
	}
	res.Changes = kept
	return res
}

func printShellResult(res shell.Result) {
	for _, c := range res.Changes {
		switch c.Action {
		case shell.Created:
			utils.PrintMessage("Created %s", c.Path)
		case shell.Appended:
			utils.PrintMessage("Added the setup to %s", c.Path)
		case shell.Updated:
			utils.PrintMessage("Updated the setup in %s", c.Path)
		case shell.Unchanged:
			utils.PrintMessage("The setup in %s is up to date", c.Path)
		case shell.Removed:
			utils.PrintMessage("Removed the setup from %s", c.Path)
		case shell.Absent:
			utils.PrintMessage("No setup in %s", c.Path)
		}
	}
	for _, w := range res.Warnings {
		utils.PrintWarning("%s", w)
	}
}

// completionAlias is an extra command name to register completion for (--alias).
var completionAlias string

// shellFromPath maps an executable path to a supported shell name, matching on
// the base name so a path like /home/zsh-fan/bin/foo does not match.
// Returns "" for anything else.
func shellFromPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(base, "fish"):
		return "fish"
	case strings.Contains(base, "zsh"):
		return "zsh"
	case strings.Contains(base, "bash"):
		return "bash"
	}
	return ""
}

// detectCompletionShell detects which shell is requesting completions from the
// parent process via /proc, falling back to $SHELL only when /proc is unreadable.
//
// An unrecognized parent is reported as unsupported rather than guessed at: the
// output is meant to be sourced, so emitting the wrong shell's script turns a
// clear error into a screenful of syntax errors in the user's login.
func detectCompletionShell() (string, error) {
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", os.Getppid())); err == nil {
		if shell := shellFromPath(exe); shell != "" {
			return shell, nil
		}
		return "", fmt.Errorf("no supported shell detected (parent process: %s)", filepath.Base(exe))
	}
	if shell := shellFromPath(os.Getenv("SHELL")); shell != "" {
		return shell, nil
	}
	return "", fmt.Errorf("could not detect the current shell")
}

// newCompletionCmd builds the completion command. The top-level one is hidden and runs the same code as `shell completion`.
func newCompletionCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Hidden: hidden,
		Use:    "completion [flags] [bash|zsh|fish]",
		Short:  "Generate shell completion script",
		Long: `Generate a shell completion script for condatainer.

- Without a shell argument, the current shell is detected.
- An alias takes effect only when the script is loaded from your shell rc file.
- 'condatainer shell init' writes the lines that load it.`,
		Example: `  Bash:
  # Current session
  source <(condatainer shell completion bash)

  Zsh:
  # Current session
  source <(condatainer shell completion zsh)
  # All sessions (install once)
  condatainer shell completion zsh > "${fpath[1]}/_condatainer"

  Fish:
  # Current session
  condatainer shell completion fish | source
  # All sessions (install once)
  condatainer shell completion fish > ~/.config/fish/completions/condatainer.fish`,
		ValidArgs: []string{"bash", "zsh", "fish"},
		Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		Run:       runCompletion,
	}
	cmd.Flags().StringVar(&completionAlias, "alias", "",
		"Define a short alias (e.g. cnt) and complete it too")
	return cmd
}

func runCompletion(cmd *cobra.Command, args []string) {
	var name string
	if len(args) > 0 {
		name = args[0]
	} else {
		detected, err := detectCompletionShell()
		if err != nil {
			ExitWithError("%v.\nSpecify it explicitly: `condatainer shell completion bash|zsh|fish`", err)
		}
		name = detected
	}

	if completionAlias != "" && !shell.ValidAlias(completionAlias) {
		ExitWithError("Invalid --alias %q: use letters, digits, '-' or '_' only.", completionAlias)
	}
	bin := cmd.Root().Name()

	switch name {
	case "bash":
		// Generate to buffer so we can post-process
		var buf bytes.Buffer
		if err := cmd.Root().GenBashCompletionV2(&buf, true); err != nil {
			_ = cmd.Root().GenBashCompletion(&buf)
		}
		// Post-process to handle -- and add overlay fzf support
		script := postProcessBashCompletion(buf.String())
		os.Stdout.WriteString(script + bashAliasBinding(bin, completionAlias))
	case "zsh":
		// Generate to buffer so we can post-process
		var buf bytes.Buffer
		cmd.Root().GenZshCompletion(&buf)
		script := postProcessZshCompletion(buf.String())
		os.Stdout.WriteString(script + zshAliasBinding(bin, completionAlias))
	case "fish":
		// Generate to buffer so we can post-process
		var buf bytes.Buffer
		cmd.Root().GenFishCompletion(&buf, true)
		script := postProcessFishCompletion(buf.String())
		os.Stdout.WriteString(script + fishAliasBinding(script, bin, completionAlias))
	}
}

// bashAliasBinding defines the alias and binds it to the generated completion
// function. Empty when no alias was requested.
func bashAliasBinding(name, alias string) string {
	if alias == "" {
		return ""
	}
	return fmt.Sprintf(`
alias %[2]s=%[1]s
if [[ $(type -t compopt) = "builtin" ]]; then
    complete -o default -F __start_%[1]s %[2]s
else
    complete -o default -o nospace -F __start_%[1]s %[2]s
fi
`, name, alias)
}

// zshAliasBinding defines the alias and binds it to the generated completion
// function. Empty when no alias was requested.
func zshAliasBinding(name, alias string) string {
	if alias == "" {
		return ""
	}
	return fmt.Sprintf("\nalias %[2]s=%[1]s\ncompdef _%[1]s %[2]s\n", name, alias)
}

// fishAliasBinding mirrors the script's 'complete -c <name>' lines onto alias.
//   - Fish binds each command separately, so the lines are duplicated verbatim.
//   - Empty when no alias was requested.
func fishAliasBinding(script, name, alias string) string {
	if alias == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nalias %s %s\n", alias, name)
	prefix := "complete -c " + name + " "
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, prefix) {
			fmt.Fprintf(&b, "complete -c %s %s\n", alias, strings.TrimPrefix(line, prefix))
		}
	}
	return b.String()
}

// bashCompletionFallback defines minimal stubs for _get_comp_words_by_ref and
// _init_completion so that the generated script works on systems that do not
// have the bash-completion package installed.
const bashCompletionFallback = `
# Minimal fallback for systems without the bash-completion package
if ! type _get_comp_words_by_ref &>/dev/null; then
    _get_comp_words_by_ref() {
        while [[ $# -gt 0 ]]; do
            case $1 in
                -n) shift 2 ;;
                cur)   cur=${COMP_WORDS[COMP_CWORD]} ;;
                prev)  prev=${COMP_WORDS[COMP_CWORD-1]} ;;
                words) words=("${COMP_WORDS[@]}") ;;
                cword) cword=$COMP_CWORD ;;
            esac
            shift
        done
    }
fi
if ! type _init_completion &>/dev/null; then
    _init_completion() {
        COMPREPLY=()
        while [[ $1 == -* ]]; do shift; [[ $1 == -n ]] && shift; done
        cur=${COMP_WORDS[COMP_CWORD]}
        prev=${COMP_WORDS[COMP_CWORD-1]}
        words=("${COMP_WORDS[@]}")
        cword=$COMP_CWORD
        return 0
    }
fi
`

// postProcessBashCompletion modifies the generated bash completion script
func postProcessBashCompletion(script string) string {
	// 0. Prepend fallback stubs for systems without bash-completion installed
	script = bashCompletionFallback + script

	// 1. Handle -- properly (use file completion after --)
	oldCode := `args=("${words[@]:1}")
    requestComp="${words[0]} __complete ${args[*]}"`

	newCode := `args=("${words[@]:1}")
    for word in "${words[@]}"; do
        if [[ "$word" == "--" ]]; then
            return
        fi
    done
    requestComp="${words[0]} __complete ${args[*]}"`

	script = strings.Replace(script, oldCode, newCode, 1)

	// 2. Hide short flags, then add fzf support for overlay/artifact selectors.
	// Both are injected at the same anchor so there is only one replacement to keep
	// in sync: the line where 'out' holds the candidates with the directive removed.
	fzfInject := `# Bash lists short and long flags on separate lines, doubling the candidate
    # list. Drop the short forms when completing a flag.
    if [[ -n ${out} && ${cur} == -* ]]; then
        out=$(printf '%s\n' "${out}" | grep -vE '^-[a-zA-Z0-9]([[:space:]]|$)')
    fi

    __condatainer_debug "The completions are: ${out}"

    # fzf-pick an overlay.
    # words[0] is the binary.
    if command -v fzf >/dev/null 2>&1 && [[ -n "$out" ]]; then
        local sub="${words[1]}"
        local is_overlay_cmd=false
        local fzf_header="Select overlay"
        if [[ "$sub" == "e" || "$sub" == "exec" || "$sub" == "info" || "$sub" == "export" ]]; then
            is_overlay_cmd=true
        elif [[ "$sub" == "registry" && "${words[2]}" == "push" ]]; then
            is_overlay_cmd=true
        fi

        # Only on an argument, not while the subcommand itself is being typed.
        if [[ "$cur" == -* ]] || (( COMP_CWORD <= 1 )) ||
           [[ "$sub" == "registry" && $COMP_CWORD -le 2 ]]; then
            is_overlay_cmd=false
        fi

        if [[ "$is_overlay_cmd" == "true" ]]; then
            if [[ $(echo "$out" | wc -l) -gt 1 ]]; then
                 local selection
                 selection=$(echo "$out" | fzf --reverse --header="$fzf_header" --query="$cur" --select-1 --exit-0)
                 if [[ -n "$selection" ]]; then
                     out="$selection"
                 fi
            fi
        fi
    fi`

	script = strings.Replace(script, `__condatainer_debug "The completions are: ${out}"`, fzfInject, 1)

	return script
}

// postProcessZshCompletion modifies the generated zsh completion script
// EXPERIMENTAL: zsh completion is not tested and may have edge cases. Feedback welcome.
func postProcessZshCompletion(script string) string {
	// Add fzf support for overlay/artifact selectors.
	fzfInject := `    __condatainer_debug "completions: ${out}"

    # fzf-pick an overlay.
    # words[1] is the binary in zsh.
    if command -v fzf >/dev/null 2>&1 && [[ -n "$out" ]]; then
        local sub="${words[2]}"
        local is_overlay_cmd=false
        local fzf_header="Select overlay"
        if [[ "$sub" == "e" || "$sub" == "exec" || "$sub" == "info" || "$sub" == "export" ]]; then
            is_overlay_cmd=true
        elif [[ "$sub" == "registry" && "${words[3]}" == "push" ]]; then
            is_overlay_cmd=true
        fi

        # Only on an argument, not while the subcommand itself is being typed.
        if [[ "${words[$CURRENT]}" == -* ]] || (( CURRENT <= 2 )) ||
           [[ "$sub" == "registry" && $CURRENT -le 3 ]]; then
            is_overlay_cmd=false
        fi

        if [[ "$is_overlay_cmd" == "true" ]]; then
             local -a lines
             lines=("${(@f)out}")
             if [[ ${#lines} -gt 1 ]]; then
                 zle -I
                 local selection
                 selection=$(echo "$out" | fzf --height 40% --reverse --header="$fzf_header" --query="$lastParam" --select-1 --exit-0)
                 if [[ -n "$selection" ]]; then
                     out="$selection"
                 fi
             fi
        fi
    fi`

	script = strings.Replace(script, `__condatainer_debug "completions: ${out}"`, fzfInject, 1)

	return script
}

// postProcessFishCompletion modifies the generated fish completion script
// EXPERIMENTAL: fish completion is not tested and may have edge cases. Feedback welcome.
func postProcessFishCompletion(script string) string {
	// Add fzf support if available for 'e/exec'
	// Note: We try to match the line where results are captured.
	// Cobra sometimes generates 'eval' and sometimes not depending on version.
	// We handle the standard pattern found in recent versions.
	// Match cobra v1.10.x generated format (eval, no extra redirect)
	target := `set -l results (eval $requestComp 2> /dev/null)`
	if !strings.Contains(script, target) {
		// Older cobra without 'eval'
		target = `set -l results ($requestComp 2> /dev/null)`
	}

	fzfInject := target + `

    # fzf-pick an overlay.
    # args[1] is the binary in fish.
    if type -q fzf
        set -l _is_overlay_cmd false
        set -l _fzf_header "Select overlay"
        if contains -- "$args[2]" e exec info export
            set _is_overlay_cmd true
        else if test "$args[2]" = registry; and test "$args[3]" = push
            set _is_overlay_cmd true
        end
        if test $_is_overlay_cmd = true
            if not string match -q -- "-*" (commandline -t)
                # Check if -- is in the command line; if so, skip fzf and use default results
                if not contains -- "--" $args
                    set -l candidates $results[1..-2]
                    if test (count $candidates) -gt 1
                        set -l selection (string join \n $candidates | fzf --height 40% --reverse --select-1 --exit-0 --query (commandline -t) --header "$_fzf_header")
                        if test -n "$selection"
                            set results $selection $results[-1]
                        end
                    end
                end
            end
        end
    end`

	return strings.Replace(script, target, fzfInject, 1)
}
