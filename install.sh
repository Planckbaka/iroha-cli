#!/bin/sh
# iroha-code One-line Installer
# Automatically detects OS and CPU architecture, downloads the latest release from GitHub, and installs it.

set -e

# Repository configuration
REPO="Planckbaka/iroha-cli"
BINARY_NAME="iroha"

# Color support for beautiful logging
reset="\033[0m"
bold="\033[1m"
green="\033[32m"
yellow="\033[33m"
red="\033[31m"
cyan="\033[36m"

info() {
    printf "${cyan}info:${reset} %s\n" "$1"
}

success() {
    printf "${green}${bold}success:${reset} %s\n" "$1"
}

warn() {
    printf "${yellow}warning:${reset} %s\n" "$1"
}

error() {
    printf "${red}${bold}error:${reset} %s\n" "$1" >&2
    exit 1
}

# 1. Detect Operating System and CPU architecture
info "Detecting system environment..."
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
    darwin)
        PLATFORM="darwin"
        ;;
    linux)
        PLATFORM="linux"
        ;;
    *)
        error "Unsupported operating system: $OS. iroha currently supports macOS and Linux."
        ;;
esac

case "$ARCH" in
    x86_64|amd64)
        CPU="amd64"
        ;;
    arm64|aarch64)
        CPU="arm64"
        ;;
    *)
        error "Unsupported CPU architecture: $ARCH. iroha supports amd64 (x86_64) and arm64."
        ;;
esac

info "Detected platform: ${bold}${PLATFORM}/${CPU}${reset}"

# 2. Fetch the latest release version from GitHub API
info "Fetching latest release version from GitHub..."
LATEST_RELEASE_URL="https://api.github.com/repos/${REPO}/releases/latest"

# Attempt to fetch version using curl or wget
if command -v curl >/dev/null 2>&1; then
    VERSION_TAG=$(curl -s "$LATEST_RELEASE_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
elif command -v wget >/dev/null 2>&1; then
    VERSION_TAG=$(wget -qO- "$LATEST_RELEASE_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
else
    error "Either 'curl' or 'wget' is required to fetch release info. Please install one of them."
fi

if [ -z "$VERSION_TAG" ]; then
    # Fallback to a default if API limits are hit
    VERSION_TAG="v1.3.0"
    warn "Could not fetch latest release tag due to rate-limiting; falling back to default: $VERSION_TAG"
fi

# Strip the leading 'v' for standard tarball naming if needed
VERSION=$(echo "$VERSION_TAG" | sed 's/^v//')
info "Target release: ${bold}${VERSION_TAG}${reset}"

# 3. Construct download URL (assuming GoReleaser naming format: agent-cli_1.3.0_darwin_arm64.tar.gz)
# We will align binary name template in GoReleaser to iroha, but for now we download the archive:
ARCHIVE_NAME="agent-cli_${VERSION}_${PLATFORM}_${CPU}.tar.gz"
DOWNLOAD_URL="https://github.com/FormatBaka/iroha-cli/releases/download/${VERSION_TAG}/${ARCHIVE_NAME}"

# Fallback download URLs to Planckbaka repository as primary
DOWNLOAD_URL="https://github.com/Planckbaka/iroha-cli/releases/download/${VERSION_TAG}/${ARCHIVE_NAME}"

# Create temporary directory
TMP_DIR=$(mktemp -d)
clean_up() {
    rm -rf "$TMP_DIR"
}
trap clean_up EXIT

# 4. Download tarball
info "Downloading archive from $DOWNLOAD_URL..."
TARBALL_PATH="${TMP_DIR}/${ARCHIVE_NAME}"

if command -v curl >/dev/null 2>&1; then
    curl -SL --fail "$DOWNLOAD_URL" -o "$TARBALL_PATH" || error "Download failed. Please check your internet connection or the release link."
elif command -v wget >/dev/null 2>&1; then
    wget -q "$DOWNLOAD_URL" -O "$TARBALL_PATH" || error "Download failed. Please check your internet connection or the release link."
fi

# 5. Extract binary
info "Extracting archive..."
tar -xzf "$TARBALL_PATH" -C "$TMP_DIR"

# Detect GoReleaser output naming: we compiled it as 'agent-cli' in goreleaser, let's verify if the file exists
EXTRACTED_BINARY="${TMP_DIR}/agent-cli"
if [ ! -f "$EXTRACTED_BINARY" ]; then
    # Fallback checks
    EXTRACTED_BINARY="${TMP_DIR}/iroha"
    if [ ! -f "$EXTRACTED_BINARY" ]; then
        # Search for any executable file in extraction folder
        EXTRACTED_BINARY=$(find "$TMP_DIR" -type f -perm -u+x | head -n 1)
    fi
fi

if [ -z "$EXTRACTED_BINARY" ] || [ ! -f "$EXTRACTED_BINARY" ]; then
    error "Binary was not found in the downloaded archive."
fi

# 6. Determine installation directory (prefer user path ~/.local/bin or standard /usr/local/bin)
if [ -d "$HOME/.local/bin" ]; then
    INSTALL_DIR="$HOME/.local/bin"
    SUDO=""
elif [ -d "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
    # Prompt for sudo if /usr/local/bin is not writable by current user
    if [ -w "/usr/local/bin" ]; then
        SUDO=""
    else
        SUDO="sudo"
        info "Write permissions to /usr/local/bin require administrative privileges."
    fi
else
    # Fallback to local user path
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
    SUDO=""
fi

TARGET_PATH="${INSTALL_DIR}/${BINARY_NAME}"

info "Installing binary to ${bold}${TARGET_PATH}${reset}..."
$SUDO cp "$EXTRACTED_BINARY" "$TARGET_PATH"
$SUDO chmod +x "$TARGET_PATH"

# 7. Post-install verification
success "iroha code has been successfully installed!"
printf "\n"
printf "  ${bold}Binary Location:${reset} %s\n" "$TARGET_PATH"
printf "  ${bold}Version Installed:${reset} %s\n" "$VERSION_TAG"
printf "\n"

# Verify if target path is in $PATH
if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
    warn "The directory '${INSTALL_DIR}' is not in your current PATH environment variable."
    case "$SHELL" in
        */zsh)
            printf "  Please add the following line to your ~/.zshrc:\n"
            printf "  ${bold}export PATH=\"\$PATH:%s\"${reset}\n\n" "$INSTALL_DIR"
            ;;
        */bash)
            printf "  Please add the following line to your ~/.bashrc or ~/.bash_profile:\n"
            printf "  ${bold}export PATH=\"\$PATH:%s\"${reset}\n\n" "$INSTALL_DIR"
            ;;
        *)
            printf "  Please append '%s' to your PATH variable in your profile config.\n\n" "$INSTALL_DIR"
            ;;
    esac
fi

printf "To start using the interactive agent copilot, simply type:\n"
printf "  ${green}${bold}iroha${reset}\n\n"
