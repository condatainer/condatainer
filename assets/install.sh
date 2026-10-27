#!/bin/bash
set -e

# Colors
GREEN=$'\033[0;32m'
BLUE=$'\033[0;34m'
CYAN=$'\033[0;36m'
YELLOW=$'\033[1;33m'
RED=$'\033[0;31m'
NC=$'\033[0m' # No Color

# Detect OS and Architecture
detect_platform() {
    local os arch binary_name

    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)

    # Map architecture names
    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) arch="$arch" ;;
    esac

    # Build binary name and check support
    os_arch="${os}_${arch}"
    case "${os_arch}" in
        linux_amd64|linux_arm64) ;;
        *)
            echo -e "${RED}[ERROR]${NC} Unsupported platform: ${os} ${arch}"
            echo -e "Currently only ${BLUE}linux amd64${NC} and ${BLUE}linux arm64${NC} are supported."
            exit 1
            ;;
    esac

    echo "$os_arch"
}

BINARY_NAME=condatainer_$(detect_platform)
URL_CONDATAINER="https://github.com/condatainer/condatainer/releases/latest/download/${BINARY_NAME}"

DEFAULT_BASE="${SCRATCH:-$HOME}/condatainer"

# The shell the installer was started from. The installer itself runs under bash, so $SHELL,
# the login shell, is only the fallback: the parent process is the shell the user typed in.
invoking_shell() {
    local name
    name=$(cat "/proc/$PPID/comm" 2>/dev/null)
    name=$(basename "${name#-}")
    case "$name" in bash|zsh|fish) echo "$name"; return ;; esac
    case "$(basename "${SHELL:-bash}")" in
        zsh) echo zsh ;;
        fish) echo fish ;;
        *) echo bash ;;
    esac
}

# Detect the shell and its config file early. A container starts bash, so another shell is set up together with bash.
SHELL_KIND=$(invoking_shell)
case "$SHELL_KIND" in
    zsh)  RC_FILE="${ZDOTDIR:-$HOME}/.zshrc" ;;
    fish) RC_FILE="$HOME/.config/fish/conf.d/condatainer.fish" ;;
    *)    RC_FILE="$HOME/.bashrc" ;;
esac
SHELL_FILES="$RC_FILE"
if [ "$SHELL_KIND" != bash ]; then
    SHELL_FILES="$RC_FILE and $HOME/.bashrc"
fi

echo -e "========================================"
echo -e "${BLUE}       CondaTainer Installer${NC}"
echo -e "========================================"

# ------------------------------------------------------------------
# 1. Helpers
# ------------------------------------------------------------------

# True when a terminal can really be opened: /dev/tty exists even with none attached.
has_tty() { { : < /dev/tty; } 2>/dev/null; }

get_input() {
    local prompt_text="$1"
    local default_value="$2"
    local user_val=""

    if has_tty; then
        local p_str="${CYAN}${prompt_text}${NC} [${default_value}]: "
        read -e -p "$p_str" -r user_val < /dev/tty 2> /dev/tty
    fi
    echo "${user_val:-$default_value}"
}

# Generic confirmation handler
confirm_default() {
    local prompt_text="$1"
    local default="$2" # "yes" or "no"
    local response=""

    # Auto-Yes Logic
    if [ "${CLI_YES:-false}" = "true" ]; then
        [ "$default" = "yes" ] && return 0 || return 1
    fi

    if has_tty; then
        if [ "$default" = "yes" ]; then
            printf "${CYAN}%s${NC} [Y/n]: " "$prompt_text" > /dev/tty
        else
            printf "${CYAN}%s${NC} [y/N]: " "$prompt_text" > /dev/tty
        fi
        read -r response < /dev/tty

        if [ -z "$response" ]; then
            [ "$default" = "yes" ] && return 0 || return 1
        fi

        [[ "$response" =~ ^[Yy]$ ]]
    else
        [ "$default" = "yes" ]
    fi
}

confirm_action() { confirm_default "$1" "yes"; }
confirm_action_no() { confirm_default "$1" "no"; }

download_failed() {
    rm -f "$2"
    echo -e "${RED}[ERROR]${NC} Could not download $1"
    exit 1
}

# download_file saves url at dest with the given mode (default 0755).
download_file() {
    local url="$1"
    local dest="$2"
    local mode="${3:-0755}"
    local dest_tmp="${dest}.tmp"
    echo -e "${BLUE}[INFO]${NC} Downloading $(basename "$dest")..."
    if command -v wget >/dev/null 2>&1; then
        wget -qO "$dest_tmp" "$url" || download_failed "$url" "$dest_tmp"
    elif command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$dest_tmp" || download_failed "$url" "$dest_tmp"
    else
        echo -e "${RED}[ERROR]${NC} Neither curl nor wget found."
        exit 1
    fi
    mv "$dest_tmp" "$dest"
    chmod "$mode" "$dest"
}

# fetch_releases prints the GitHub Releases listing, newest first.
fetch_releases() {
    local api_url="https://api.github.com/repos/condatainer/condatainer/releases?per_page=100"

    if command -v wget >/dev/null 2>&1; then
        wget -qO - "$api_url"
    elif command -v curl >/dev/null 2>&1; then
        curl -fsSL "$api_url"
    else
        echo -e "${RED}[ERROR]${NC} Neither curl nor wget found." >&2
        exit 1
    fi
}

# list_versions prints every released version, newest first, one per line.
# A prerelease is marked.
list_versions() {
    fetch_releases \
        | grep -oE '"tag_name": *"[^"]+"|"prerelease": *(true|false)' \
        | awk -F'"' '
            $2 == "tag_name"   { tag=$4; sub(/^v/, "", tag); next }
            $2 == "prerelease" { print tag ($0 ~ /true$/ ? " (prerelease)" : "") }'
}

# resolve_version_url points the download at one released version, given with
# or without the leading "v".
resolve_version_url() {
    local version="${1#v}"
    local versions

    versions=$(list_versions) || exit 1
    if [ -z "$versions" ]; then
        echo -e "${RED}[ERROR]${NC} No released versions found."
        exit 1
    fi
    if ! printf '%s\n' "$versions" | sed 's/ (prerelease)$//' | grep -Fxq "$version"; then
        echo -e "${RED}[ERROR]${NC} No release $version. Released versions:"
        printf '%s\n' "$versions" | sed 's/^/  /'
        exit 1
    fi

    URL_CONDATAINER="https://github.com/condatainer/condatainer/releases/download/v${version}/${BINARY_NAME}"
    echo -e "${BLUE}[INFO]${NC} Using version: ${BLUE}${version}${NC}"
}

resolve_prerelease_url() {
    local release_json tag

    release_json=$(fetch_releases) || exit 1

    tag=$(printf '%s' "$release_json" \
        | tr -d '\n' \
        | sed 's/},{/}\n{/g' \
        | awk '{
            obj=$0
            gsub(/[[:space:]]+/, "", obj)
            if (obj ~ /"prerelease":true/ && match(obj, /"tag_name":"[^"]+"/)) {
                print substr(obj, RSTART + 12, RLENGTH - 13)
                exit
            }
        }')

    if [ -z "$tag" ]; then
        echo -e "${RED}[ERROR]${NC} Could not find a prerelease tag from GitHub Releases."
        exit 1
    fi

    URL_CONDATAINER="https://github.com/condatainer/condatainer/releases/download/${tag}/${BINARY_NAME}"
    echo -e "${BLUE}[INFO]${NC} Using prerelease: ${BLUE}${tag}${NC}"
}

# CLI options parsing
CLI_PATH=""
CLI_YES=false
CLI_DEV=false
CLI_VERSION=""         # a released version; empty is the newest
CLI_NO_SHELL=false
CLI_DOWNLOAD_ONLY=false

show_usage() {
    cat <<'USAGE'
Usage: install_condatainer.sh [options]
Options:
  -p, --path PATH      Install into PATH
  -v, --version VER    Install this released version instead of the newest (e.g. 0.2.0)
  -d, --dev            Install the latest prerelease build
      --list-versions  List the released versions and exit
      --no-shell       Do not set up the shell, for a group or site install
      --download-only  Only download condatainer into PATH/bin
  -y, --yes            Assume yes for all prompts
  -h, --help           Show this help
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        -p|--path)  [ -n "$2" ] && { CLI_PATH="$2"; shift 2; } || { echo -e "${RED}[ERROR]${NC} --path requires an argument."; exit 1; } ;;
        -d|--dev)   CLI_DEV=true; shift ;;
        -v|--version) [ -n "$2" ] && { CLI_VERSION="$2"; shift 2; } || { echo -e "${RED}[ERROR]${NC} --version requires an argument."; exit 1; } ;;
        --list-versions) list_versions; exit 0 ;;
        -y|--yes)   CLI_YES=true; shift ;;
        --no-shell) CLI_NO_SHELL=true; shift ;;
        --download-only) CLI_DOWNLOAD_ONLY=true; shift ;;
        -h|--help)  show_usage; exit 0 ;;
        *)          echo -e "${RED}[ERROR]${NC} Unknown option: $1"; show_usage; exit 1 ;;
    esac
done

if [ -n "$CLI_VERSION" ] && [ "$CLI_DEV" = true ]; then
    echo -e "${RED}[ERROR]${NC} --version and --dev cannot be used together."; exit 1
fi

# ------------------------------------------------------------------
# 2. Pre-Install Checks
# ------------------------------------------------------------------

# An earlier install on PATH is updated with `condatainer self-update`, not copied again.
if [ "$CLI_DOWNLOAD_ONLY" != true ] && [ "$CLI_NO_SHELL" != true ] && command -v condatainer >/dev/null 2>&1; then
    echo -e "${YELLOW}[WARNING]${NC} 'condatainer' is already installed: $(command -v condatainer)"
    echo -e "To update it, run 'condatainer self-update'."
    if ! confirm_action_no "Install another copy?"; then
        echo "Installation aborted."; exit 1
    fi
fi

# ------------------------------------------------------------------
# 3. Configuration Phase
# ------------------------------------------------------------------

echo -e "\nConfiguration:"

if [ "${CLI_DEV}" = true ]; then
    resolve_prerelease_url
elif [ -n "$CLI_VERSION" ]; then
    resolve_version_url "$CLI_VERSION"
fi

# Determine Install Path
if [ -n "$CLI_PATH" ]; then
    INSTALL_BASE="$CLI_PATH"
elif [ "${CLI_YES}" = true ]; then
    INSTALL_BASE="$DEFAULT_BASE"
else
    INSTALL_BASE=$(get_input "Install Path" "$DEFAULT_BASE")
fi

INSTALL_BASE="${INSTALL_BASE/#\~/$HOME}"

# Resolve path
if command -v realpath >/dev/null 2>&1; then
    if RESOLVED=$(realpath -m "$INSTALL_BASE" 2>/dev/null); then
        INSTALL_BASE="$RESOLVED"
    elif [ -e "$INSTALL_BASE" ] && RESOLVED=$(realpath "$INSTALL_BASE" 2>/dev/null); then
        INSTALL_BASE="$RESOLVED"
    fi
fi
if [[ "$INSTALL_BASE" != /* ]]; then INSTALL_BASE="$PWD/$INSTALL_BASE"; fi
INSTALL_BIN="$INSTALL_BASE/bin"

# A personal bin directory is created with the default permissions, not group-writable
HOME_BIN_INSTALL=false
case "$INSTALL_BIN" in
    "$HOME/bin"|"$HOME/.local/bin")
        HOME_BIN_INSTALL=true
        ;;
esac

# Target exists check: an empty directory (e.g. prepared with group permissions)
# or an earlier install is used as is; anything else asks first.
if [ -d "$INSTALL_BASE" ]; then
    if [ -x "$INSTALL_BIN/condatainer" ]; then
        echo -e "${BLUE}[INFO]${NC} Updating the installation in ${BLUE}$INSTALL_BASE${NC}"
    elif [ -n "$(ls -A "$INSTALL_BASE" 2>/dev/null)" ]; then
        if ! confirm_action_no "Target '$INSTALL_BASE' is not empty. Continue installation?"; then
            echo "Installation aborted."; exit 1
        fi
    fi
fi

# Steps. A download-only install does nothing past the download.
DO_INIT=true
DO_SHELL=false
DO_LIBEXEC=false
if [ "$CLI_DOWNLOAD_ONLY" = true ]; then
    DO_INIT=false
else
    if [ "$CLI_NO_SHELL" != true ] && confirm_action "Set up condatainer in your shell (PATH, completion, mm) in $SHELL_FILES?"; then
        DO_SHELL=true
    fi
    if confirm_action "Download the toolchain now (apptainer, micromamba and the tools they use)?"; then
        DO_LIBEXEC=true
    fi
fi

# ------------------------------------------------------------------
# 4. Final Verification
# ------------------------------------------------------------------

echo -e "\n--------- Installation Summary ---------"
echo -e "Directory   : ${BLUE}$INSTALL_BIN${NC}"
if [ "${CLI_DEV}" = true ]; then
    echo -e "Release     : ${YELLOW}Prerelease (--dev)${NC}"
elif [ -n "$CLI_VERSION" ]; then
    echo -e "Release     : ${GREEN}${CLI_VERSION#v}${NC}"
else
    echo -e "Release     : ${GREEN}Stable${NC}"
fi
if [ "$DO_INIT" = true ]; then echo -e "Config      : ${GREEN}Yes${NC}"; else echo -e "Config      : skipped"; fi
if [ "$DO_SHELL" = true ]; then echo -e "Shell config: ${BLUE}$SHELL_FILES${NC}"; else echo -e "Shell config: skipped"; fi
if [ "$DO_LIBEXEC" = true ]; then echo -e "Toolchain   : ${GREEN}Yes${NC}"; else echo -e "Toolchain   : skipped"; fi
echo -e "----------------------------------------"

if ! confirm_action "Proceed?"; then echo "Aborted."; exit 0; fi

# ------------------------------------------------------------------
# 5. Execution Phase
# ------------------------------------------------------------------

echo -e "\nStarting installation..."

# The binary and bin/ are group-writable only in a directory that is already group-writable,
# such as one an admin prepared for a group.
BIN_MODE=0755
if [ -d "$INSTALL_BASE" ] && [ "$HOME_BIN_INSTALL" != true ] \
    && [ $(( 0$(stat -c %a "$INSTALL_BASE" 2>/dev/null || echo 0) & 020 )) -ne 0 ]; then
    BIN_MODE=0775
fi

# Prepare Directory
if [ "$BIN_MODE" = 0775 ]; then
    mkdir -p -m 0775 "$INSTALL_BIN"
else
    mkdir -p "$INSTALL_BIN"
fi

# Download condatainer
download_file "$URL_CONDATAINER" "$INSTALL_BIN/condatainer" "$BIN_MODE"

# Init the condatainer config.
if [ "$DO_INIT" = true ]; then
    CONFIG_INIT_ARGS=()
    if [ "${CLI_YES:-false}" = "true" ]; then CONFIG_INIT_ARGS+=(-y); fi
    if ! "$INSTALL_BIN/condatainer" config init "${CONFIG_INIT_ARGS[@]}"; then
        echo -e "${RED}[ERROR]${NC} Failed to initialize CondaTainer config."
        exit 1
    fi
fi

# Downloads the toolchain (apptainer with its tools, and micromamba).
# A failure is a warning: the install stays usable, and `update --libexec` retries.
if [ "$DO_LIBEXEC" = true ]; then
    if ! "$INSTALL_BIN/condatainer" update --libexec; then
        echo -e "${YELLOW}[WARN]${NC} Could not install the toolchain."
        echo "Run 'condatainer update --libexec' to retry."
    fi
elif [ "$CLI_DOWNLOAD_ONLY" != true ]; then
    echo -e "${BLUE}[INFO]${NC} Skipped the toolchain. Run 'condatainer update --libexec' to install it."
fi

# Set up the shell (only when not declined). A failure is a warning:
# the install stays usable, and `condatainer shell init` retries.
if [ "$DO_SHELL" = true ]; then
    if ! "$INSTALL_BIN/condatainer" shell init --shell "$SHELL_KIND"; then
        echo -e "${YELLOW}[WARN]${NC} Could not set up your shell."
        echo "Run '$INSTALL_BIN/condatainer shell init' to retry."
        DO_SHELL=false
    fi
fi

echo -e "----------------------------------------"
echo -e "${GREEN}Success!${NC}"
echo -e "Installed: ${BLUE}$INSTALL_BIN/condatainer${NC}"
if [ "$CLI_DOWNLOAD_ONLY" != true ] && [ "$CLI_NO_SHELL" != true ]; then
    if [ "$DO_SHELL" = true ] && [ "$SHELL_KIND" = fish ]; then
        echo "Open a new terminal to apply changes."
    elif [ "$DO_SHELL" = true ]; then
        echo "Run this to apply changes:"
        echo -e "  ${YELLOW}source $RC_FILE${NC}"
    else
        echo "Set up your shell (PATH, completion, mm) when you are ready:"
        echo -e "  ${YELLOW}$INSTALL_BIN/condatainer shell init${NC}"
    fi
fi
echo -e "========================================"
