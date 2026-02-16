#!/bin/sh
# GoClaw Installer
# Usage: curl -fsSL https://goclaw.org/install.sh | sh
#    or: curl -fsSL https://goclaw.org/install.sh | sh -s -- --channel beta
#
# Options:
#   --version VERSION   Install specific version (default: latest)
#   --channel CHANNEL   Install from channel: stable, beta (default: stable)
#   --no-path           Skip PATH configuration
#   --help              Show this help

set -e

REPO="roelfdiedericks/goclaw"
INSTALL_DIR="$HOME/.goclaw/bin"
BINARY_NAME="goclaw"

# Defaults
VERSION=""
CHANNEL="stable"
SKIP_PATH=false

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
        --help|-h)
            usage
            ;;
        *)
            error "Unknown option: $1. Use --help for usage."
            ;;
    esac
done

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

# Configure PATH
configure_path() {
    if [ "$SKIP_PATH" = true ]; then
        return
    fi
    
    os="$1"
    binary_path="$INSTALL_DIR/$BINARY_NAME"
    
    # Check if already in PATH
    if echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
        info "Install directory already in PATH"
        return
    fi
    
    # Linux: Try symlink to ~/.local/bin first
    if [ "$os" = "linux" ]; then
        local_bin="$HOME/.local/bin"
        if [ -d "$local_bin" ] && echo "$PATH" | tr ':' '\n' | grep -qx "$local_bin"; then
            info "Creating symlink in ~/.local/bin..."
            mkdir -p "$local_bin"
            ln -sf "$binary_path" "$local_bin/$BINARY_NAME"
            success "Symlink created: $local_bin/$BINARY_NAME"
            echo ""
            success "GoClaw is ready! Run: goclaw setup"
            return
        fi
    fi
    
    # Fallback: Modify shell RC file
    export_line="export PATH=\"\$PATH:$INSTALL_DIR\""
    
    # Detect shell and RC file
    shell_name=$(basename "$SHELL")
    case "$shell_name" in
        bash)
            rc_file="$HOME/.bashrc"
            ;;
        zsh)
            rc_file="$HOME/.zshrc"
            ;;
        fish)
            rc_file="$HOME/.config/fish/config.fish"
            export_line="set -gx PATH \$PATH $INSTALL_DIR"
            ;;
        *)
            rc_file="$HOME/.profile"
            ;;
    esac
    
    # Check if already configured
    if [ -f "$rc_file" ] && grep -q "$INSTALL_DIR" "$rc_file"; then
        info "PATH already configured in $rc_file"
    else
        info "Adding GoClaw to PATH in $rc_file..."
        echo "" >> "$rc_file"
        echo "# GoClaw" >> "$rc_file"
        echo "$export_line" >> "$rc_file"
        success "PATH configured in $rc_file"
    fi
    
    echo ""
    warn "To use goclaw in this terminal, run:"
    echo ""
    printf "    ${GREEN}source %s${NC}\n" "$rc_file"
    echo ""
    echo "Then run: goclaw setup"
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
    "$INSTALL_DIR/$BINARY_NAME" --version 2>/dev/null || true
    
    # Configure PATH
    configure_path "$OS"
    
    echo ""
    success "Installation complete!"
}

main "$@"
