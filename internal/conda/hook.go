package conda

import (
	"fmt"
	"os"
)

// EnvMounted reports whether this process runs in a container with an environment overlay mounted.
func EnvMounted() bool {
	return os.Getenv("IN_CONDATAINER") != "" && os.Getenv("CNT_CONDA_ROOT") != ""
}

// MMFunction returns the mm shell function for shell.
//   - mm runs `condatainer env` with its arguments.
//   - After install, update or remove it evaluates `condatainer env reactivate` for the same shell.
func MMFunction(shell Shell) string {
	if shell == ShellFish {
		return `function mm
    condatainer env $argv; or return
    switch $argv[1]
        case install update remove
            condatainer env reactivate --shell fish | source
    end
end
`
	}
	return fmt.Sprintf(`mm() {
    condatainer env "$@" || return
    case "$1" in
        install|update|remove) eval "$(condatainer env reactivate --shell %s)" ;;
    esac
}
`, shell)
}

// MMCompletion returns the snippet that completes mm as `condatainer env` is completed.
//   - bash and zsh rewrite the typed words to `condatainer env ...` and call the completion function `shell completion` defines, when Tab is pressed.
//   - zsh binds only when compdef exists, so a .zshrc that runs compinit later does not fail.
func MMCompletion(shell Shell) string {
	switch shell {
	case ShellBash:
		return `_mm_complete() {
    COMP_WORDS=(condatainer env "${COMP_WORDS[@]:1}")
    COMP_CWORD=$((COMP_CWORD + 1))
    __start_condatainer
}
if [[ $(type -t compopt) = "builtin" ]]; then
    complete -o default -F _mm_complete mm
else
    complete -o default -o nospace -F _mm_complete mm
fi
`
	case ShellZsh:
		return `_mm() { words=(condatainer env "${(@)words[2,-1]}"); (( CURRENT++ )); _condatainer }
(( $+functions[compdef] )) && compdef _mm mm
`
	case ShellFish:
		return "complete -c mm --wraps 'condatainer env'\n"
	}
	return ""
}

// MMHook returns what `condatainer env hook` prints: the mm function and its completion, or "" when no environment is mounted.
func MMHook(shell Shell) string {
	if !EnvMounted() {
		return ""
	}
	return MMFunction(shell) + MMCompletion(shell)
}
