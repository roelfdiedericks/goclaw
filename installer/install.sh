#!/bin/sh
# GoClaw Installer
# Usage: curl -fsSL https://goclaw.org/install.sh | sh
#    or: curl -fsSL https://goclaw.org/install.sh | sh -s -- --channel beta
#
# Options:
#   --version VERSION   Install specific version (default: latest)
#   --channel CHANNEL   Install from channel: stable, beta (default: stable)
#   --deps MODE         Dependency install mode: auto, install, skip (default: auto)
#   --yes               Alias for --deps install
#   --no-deps           Alias for --deps skip
#   --allow-root        Allow install as root without interactive confirmation
#   --no-path           Skip PATH configuration
#   --help              Show this help

set -e

REPO="roelfdiedericks/goclaw"
INSTALL_DIR="$HOME/.goclaw/bin"
BINARY_NAME="goclaw"
USER_BIN_DIR=""
RC_FILE=""
RC_EXPORT_LINE=""
PATH_ACTION="none"
HAD_EXISTING_BINARY=false
HAD_EXISTING_CONFIG=false
EXISTING_BINARY_PATH=""

# Defaults
VERSION=""
CHANNEL="stable"
SKIP_PATH=false
DEPS_MODE="auto"
ALLOW_ROOT=false

# Colors (disabled if not a terminal)
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    BLUE='\033[0;34m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    NC=''
fi

info() { printf "${BLUE}==>${NC} %s\n" "$1"; }
success() { printf "${GREEN}==>${NC} %s\n" "$1"; }
warn() { printf "${YELLOW}Warning:${NC} %s\n" "$1"; }
error() { printf "${RED}Error:${NC} %s\n" "$1" >&2; exit 1; }

usage() {
    cat <<EOF
GoClaw Installer

Usage: curl -fsSL https://goclaw.org/install.sh | sh
   or: curl -fsSL https://goclaw.org/install.sh | sh -s -- [OPTIONS]

Options:
    --version VERSION   Install specific version (e.g., 0.1.0)
    --channel CHANNEL   Install from channel: stable, beta (default: stable)
    --deps MODE         Dependency install mode: auto, install, skip (default: auto)
    --yes               Alias for --deps install
    --no-deps           Alias for --deps skip
    --allow-root        Allow install as root without interactive confirmation
    --no-path           Skip PATH configuration
    --help              Show this help

Examples:
    # Install latest stable
    curl -fsSL https://goclaw.org/install.sh | sh

    # Install latest beta
    curl -fsSL https://goclaw.org/install.sh | sh -s -- --channel beta

    # Install specific version
    curl -fsSL https://goclaw.org/install.sh | sh -s -- --version 0.2.0
EOF
    exit 0
}

# Parse arguments
while [ $# -gt 0 ]; do
    case "$1" in
        --version)
            VERSION="$2"
            shift 2
            ;;
        --channel)
            CHANNEL="$2"
            shift 2
            ;;
        --no-path)
            SKIP_PATH=true
            shift
            ;;
        --deps)
            DEPS_MODE="$2"
            shift 2
            ;;
        --yes)
            DEPS_MODE="install"
            shift
            ;;
        --no-deps)
            DEPS_MODE="skip"
            shift
            ;;
        --allow-root)
            ALLOW_ROOT=true
            shift
            ;;
        --help|-h)
            usage
            ;;
        *)
            error "Unknown option: $1. Use --help for usage."
            ;;
    esac
done

case "$DEPS_MODE" in
    auto|install|skip) ;;
    *) error "Invalid --deps mode: $DEPS_MODE (expected: auto, install, skip)" ;;
esac

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        *)       error "Unsupported OS: $(uname -s)" ;;
    esac
}

# Detect architecture
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo "amd64" ;;
        aarch64|arm64)  echo "arm64" ;;
        *)              error "Unsupported architecture: $(uname -m)" ;;
    esac
}

# Check for required commands
check_dependencies() {
    for cmd in curl tar; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            error "Required command not found: $cmd"
        fi
    done
}

# Get latest release version for a channel
get_latest_version() {
    channel="$1"
    
    if [ "$channel" = "stable" ]; then
        # Get latest non-prerelease
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | \
            grep '"tag_name"' | sed 's/.*"v\([^"]*\)".*/\1/' | head -1
    else
        # Get latest prerelease matching channel (e.g., beta)
        curl -fsSL "https://api.github.com/repos/${REPO}/releases" | \
            grep '"tag_name"' | sed 's/.*"v\([^"]*\)".*/\1/' | \
            grep "${channel}" | head -1
    fi
}

# Compute checksum (cross-platform)
compute_checksum() {
    file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    else
        error "No sha256sum or shasum available for checksum verification"
    fi
}

# Verify checksum against checksums.txt
verify_checksum() {
    file="$1"
    expected_name="$2"
    checksums_url="$3"
    
    info "Verifying checksum..."
    
    checksums=$(curl -fsSL "$checksums_url") || error "Failed to download checksums"
    expected=$(echo "$checksums" | grep "$expected_name" | awk '{print $1}')
    
    if [ -z "$expected" ]; then
        error "Checksum not found for $expected_name"
    fi
    
    actual=$(compute_checksum "$file")
    
    if [ "$actual" != "$expected" ]; then
        error "Checksum mismatch!\n  Expected: $expected\n  Got:      $actual"
    fi
    
    success "Checksum verified"
}

# Return 0 if a directory is writable by the current user, 1 otherwise.
is_writable_dir() {
    dir="$1"
    [ -n "$dir" ] || return 1
    [ -d "$dir" ] || return 1
    [ -w "$dir" ]
}

# Choose an existing user-owned bin directory already present in PATH.
choose_user_bin_dir() {
    OLD_IFS=$IFS
    IFS=':'
    for dir in $PATH; do
        case "$dir" in
            "$HOME"/*)
                case "$dir" in
                    */bin)
                        if is_writable_dir "$dir"; then
                            printf '%s\n' "$dir"
                            IFS=$OLD_IFS
                            return 0
                        fi
                        ;;
                esac
                ;;
        esac
    done
    IFS=$OLD_IFS
    return 1
}

# Detect the user's preferred shell name.
detect_shell_name() {
    if [ -n "$SHELL" ]; then
        basename "$SHELL"
        return 0
    fi
    return 1
}

# Determine the most conservative rc file to update for the detected shell.
select_rc_file() {
    os="$1"
    shell_name="$2"

    case "$shell_name" in
        bash)
            if [ "$os" = "darwin" ]; then
                if [ -f "$HOME/.bash_profile" ]; then
                    printf '%s\n' "$HOME/.bash_profile"
                elif [ -f "$HOME/.bash_login" ]; then
                    printf '%s\n' "$HOME/.bash_login"
                else
                    printf '%s\n' "$HOME/.bash_profile"
                fi
            else
                if [ -f "$HOME/.bashrc" ]; then
                    printf '%s\n' "$HOME/.bashrc"
                else
                    printf '%s\n' "$HOME/.bashrc"
                fi
            fi
            ;;
        zsh)
            printf '%s\n' "$HOME/.zshrc"
            ;;
        fish)
            printf '%s\n' "$HOME/.config/fish/config.fish"
            ;;
        *)
            return 1
            ;;
    esac
}

# Decide how the installed binary should be exposed to the user's PATH.
choose_path_strategy() {
    os="$1"

    if [ "$SKIP_PATH" = true ]; then
        PATH_ACTION="skip"
        return
    fi

    if echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
        PATH_ACTION="ready"
        return
    fi

    if USER_BIN_DIR=$(choose_user_bin_dir); then
        PATH_ACTION="symlink"
        return
    fi

    shell_name=$(detect_shell_name 2>/dev/null || true)
    if [ -n "$shell_name" ]; then
        if RC_FILE=$(select_rc_file "$os" "$shell_name" 2>/dev/null); then
            PATH_ACTION="rc"
            case "$shell_name" in
                fish)
                    RC_EXPORT_LINE="set -gx PATH $INSTALL_DIR \$PATH"
                    ;;
                *)
                    RC_EXPORT_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
                    ;;
            esac
            return
        fi
    fi

    PATH_ACTION="manual"
}

detect_existing_install_state() {
    if [ -x "$INSTALL_DIR/$BINARY_NAME" ]; then
        HAD_EXISTING_BINARY=true
        EXISTING_BINARY_PATH="$INSTALL_DIR/$BINARY_NAME"
    else
        existing_path=$(command -v "$BINARY_NAME" 2>/dev/null || true)
        if [ -n "$existing_path" ] && [ -x "$existing_path" ]; then
            HAD_EXISTING_BINARY=true
            EXISTING_BINARY_PATH="$existing_path"
        fi
    fi

    if [ -s "$HOME/.goclaw/goclaw.json" ]; then
        HAD_EXISTING_CONFIG=true
    fi
}

print_post_install_guidance() {
    command_prefix="$1"
    if [ "$HAD_EXISTING_CONFIG" = true ]; then
        success "Existing GoClaw configuration detected."
        echo "Guided setup: ${command_prefix} onboard"
        echo "Edit current config: ${command_prefix} setup edit"
        return
    fi

    success "GoClaw is ready! Run: ${command_prefix} onboard"
    echo "This walks you through first-time setup."
}

# Configure PATH
configure_path() {
    if [ "$SKIP_PATH" = true ]; then
        return
    fi
    
    os="$1"
    binary_path="$INSTALL_DIR/$BINARY_NAME"

    choose_path_strategy "$os"

    case "$PATH_ACTION" in
        ready)
            info "Install directory already in PATH"
            ;;
        symlink)
            info "Creating symlink in $USER_BIN_DIR..."
            ln -sf "$binary_path" "$USER_BIN_DIR/$BINARY_NAME"
            success "Symlink created: $USER_BIN_DIR/$BINARY_NAME"
            ;;
        rc)
            if [ -f "$RC_FILE" ] && grep -q "$INSTALL_DIR" "$RC_FILE"; then
                info "PATH already configured in $RC_FILE"
            else
                info "Adding GoClaw to PATH in $RC_FILE..."
                rc_parent=$(dirname "$RC_FILE")
                mkdir -p "$rc_parent"
                if [ ! -f "$RC_FILE" ]; then
                    : > "$RC_FILE"
                fi
                echo "" >> "$RC_FILE"
                echo "# GoClaw" >> "$RC_FILE"
                echo "$RC_EXPORT_LINE" >> "$RC_FILE"
                success "PATH configured in $RC_FILE"
            fi
            ;;
        manual)
            ;;
    esac
}

# Prompt user via /dev/tty so curl|sh doesn't consume script stdin.
# Return codes: 0=yes, 1=no, 2=no tty available.
prompt_yes_no_tty() {
    prompt="$1"
    if [ ! -r /dev/tty ] || [ ! -w /dev/tty ]; then
        return 2
    fi

    printf "%b" "$prompt" > /dev/tty
    if IFS= read -r confirm < /dev/tty; then
        if [ "$confirm" = "y" ] || [ "$confirm" = "Y" ]; then
            return 0
        fi
        return 1
    fi
    return 1
}

# Root installs are discouraged for security; require explicit confirmation.
confirm_root_install() {
    if [ "$(id -u)" -ne 0 ]; then
        return
    fi

    if [ "$ALLOW_ROOT" = true ]; then
        warn "Running installer as root (allowed by --allow-root)."
        return
    fi

    echo ""
    warn "SECURITY WARNING: running GoClaw as root is not recommended."
    warn "This increases risk and may expose your full system to agent/tool actions."
    warn "Install and run GoClaw as a normal user whenever possible."
    echo ""

    if prompt_yes_no_tty "Continue installing as root into ${GREEN}${INSTALL_DIR}${NC}? [y/N] "; then
        warn "Continuing as root by explicit confirmation."
        return
    fi

    rc=$?
    if [ "$rc" -eq 2 ]; then
        error "Root install requires interactive confirmation. Re-run with --allow-root to bypass prompt."
    fi
    error "Aborted root install."
}

# Main installation
main() {
    echo ""
    printf "${BLUE}GoClaw Installer${NC}\n"
    echo ""
    
    check_dependencies
    
    OS=$(detect_os)
    ARCH=$(detect_arch)
    
    info "Detected: $OS/$ARCH"
    confirm_root_install
    detect_existing_install_state
    if [ "$HAD_EXISTING_BINARY" = true ]; then
        info "Existing GoClaw binary detected at $EXISTING_BINARY_PATH"
    fi
    if [ "$HAD_EXISTING_CONFIG" = true ]; then
        info "Existing GoClaw configuration detected at $HOME/.goclaw/goclaw.json"
    fi
    
    # Determine version to install
    if [ -z "$VERSION" ]; then
        info "Finding latest $CHANNEL release..."
        VERSION=$(get_latest_version "$CHANNEL")
        if [ -z "$VERSION" ]; then
            error "No $CHANNEL release found"
        fi
    fi
    
    info "Installing version: $VERSION"
    
    # Construct download URLs
    # GoReleaser format: goclaw_VERSION_OS_ARCH.tar.gz
    archive_name="goclaw_${VERSION}_${OS}_${ARCH}.tar.gz"
    
    # Determine tag (stable = vX.Y.Z, beta = vX.Y.Z-beta.N)
    if echo "$VERSION" | grep -q "-"; then
        tag="v${VERSION}"
    else
        tag="v${VERSION}"
    fi
    
    base_url="https://github.com/${REPO}/releases/download/${tag}"
    archive_url="${base_url}/${archive_name}"
    checksums_url="${base_url}/checksums.txt"
    
    # Create temp directory
    tmp_dir=$(mktemp -d)
    trap "rm -rf $tmp_dir" EXIT
    
    # Download archive
    info "Downloading $archive_name..."
    curl -fsSL "$archive_url" -o "$tmp_dir/$archive_name" || \
        error "Failed to download $archive_url"
    
    # Verify checksum
    verify_checksum "$tmp_dir/$archive_name" "$archive_name" "$checksums_url"
    
    # Extract
    info "Extracting..."
    tar -xzf "$tmp_dir/$archive_name" -C "$tmp_dir"
    
    # Install
    info "Installing to $INSTALL_DIR..."
    mkdir -p "$INSTALL_DIR"
    mv "$tmp_dir/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
    chmod +x "$INSTALL_DIR/$BINARY_NAME"
    
    success "Installed: $INSTALL_DIR/$BINARY_NAME"
    
    # Show version
    "$INSTALL_DIR/$BINARY_NAME" version 2>/dev/null || true
    
    # Configure PATH
    configure_path "$OS"
    
    # Install runtime dependencies (Linux only)
    install_dependencies "$OS"
    
    echo ""
    success "Installation complete!"
    echo ""
    case "$PATH_ACTION" in
        skip)
            warn "PATH configuration skipped (--no-path)."
            if [ "$HAD_EXISTING_CONFIG" = true ]; then
                printf "Guided setup: ${GREEN}%s onboard${NC}\n" "$INSTALL_DIR/$BINARY_NAME"
                printf "Edit current config: ${GREEN}%s setup edit${NC}\n" "$INSTALL_DIR/$BINARY_NAME"
            else
                printf "Run it directly: ${GREEN}%s onboard${NC}\n" "$INSTALL_DIR/$BINARY_NAME"
            fi
            ;;
        ready)
            print_post_install_guidance "goclaw"
            ;;
        symlink)
            print_post_install_guidance "goclaw"
            ;;
        rc)
            warn "To use goclaw in this terminal, run:"
            echo ""
            printf "    ${GREEN}source %s${NC}\n" "$RC_FILE"
            echo ""
            echo "Then run:"
            if [ "$HAD_EXISTING_CONFIG" = true ]; then
                echo "  - goclaw onboard"
                echo "  - goclaw setup edit"
            else
                echo "  - goclaw onboard"
            fi
            ;;
        manual)
            warn "Could not determine a safe shell startup file to update."
            echo "Add this to your shell configuration manually:"
            echo ""
            printf "    ${GREEN}export PATH=\"\$PATH:%s\"${NC}\n" "$INSTALL_DIR"
            echo ""
            echo "Then run:"
            if [ "$HAD_EXISTING_CONFIG" = true ]; then
                echo "  - goclaw onboard"
                echo "  - goclaw setup edit"
            else
                echo "  - goclaw onboard"
            fi
            ;;
    esac
}

# Install runtime dependencies (Linux only)
install_dependencies() {
    os="$1"
    
    if [ "$os" != "linux" ]; then
        return
    fi
    
    echo ""
    info "Checking runtime dependencies..."
    
    missing=""
    if ! command -v bwrap >/dev/null 2>&1; then
        if [ -z "$missing" ]; then missing="bubblewrap"; else missing="$missing bubblewrap"; fi
    fi
    if ! command -v ffmpeg >/dev/null 2>&1; then
        if [ -z "$missing" ]; then missing="ffmpeg"; else missing="$missing ffmpeg"; fi
    fi
    
    if [ -z "$missing" ]; then
        success "All dependencies installed"
        return
    fi
    
    info "Optional dependencies not found: $missing"
    info "These enable sandboxed execution and audio processing."
    
    # Detect package manager
    pkg_cmd=""
    if command -v apt-get >/dev/null 2>&1; then
        pkg_cmd="apt-get install -y"
    elif command -v dnf >/dev/null 2>&1; then
        pkg_cmd="dnf install -y"
    elif command -v pacman >/dev/null 2>&1; then
        pkg_cmd="pacman -S --noconfirm"
    elif command -v apk >/dev/null 2>&1; then
        pkg_cmd="apk add"
    fi
    
    if [ -z "$pkg_cmd" ]; then
        warn "Could not detect package manager"
        echo "Please install manually: $missing"
        return
    fi

    install_cmd="$pkg_cmd $missing"
    run_cmd="$install_cmd"
    can_auto_install=true
    if [ "$(id -u)" -ne 0 ]; then
        if command -v sudo >/dev/null 2>&1; then
            install_cmd="sudo $install_cmd"
            run_cmd="sudo $run_cmd"
        else
            can_auto_install=false
        fi
    fi

    should_install=false
    case "$DEPS_MODE" in
        install)
            if [ "$can_auto_install" = true ]; then
                should_install=true
            else
                warn "Cannot auto-install dependencies without root privileges or sudo."
            fi
            ;;
        skip)
            ;;
        auto)
            if [ "$can_auto_install" = true ]; then
                if prompt_yes_no_tty "Install with: ${GREEN}${install_cmd}${NC}? [y/N] "; then
                    should_install=true
                else
                    rc=$?
                    if [ "$rc" -eq 2 ]; then
                        info "No interactive terminal detected; skipping dependency install."
                    fi
                fi
            else
                warn "Cannot auto-install dependencies without root privileges or sudo."
            fi
            ;;
    esac

    if [ "$should_install" = true ]; then
        # shellcheck disable=SC2086
        $run_cmd && success "Dependencies installed" || warn "Some dependencies failed to install"
    else
        echo "Skipped. Install manually if needed: $missing"
        if [ "$(id -u)" -eq 0 ]; then
            echo "Run: $pkg_cmd $missing"
        else
            if command -v sudo >/dev/null 2>&1; then
                echo "Run: sudo $pkg_cmd $missing"
            else
                echo "Run as root: $pkg_cmd $missing"
            fi
        fi
    fi
}

main "$@"
