#!/usr/bin/env bash
#
# claude-cowork-service installer
# https://github.com/patrickjaja/claude-cowork-service
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/patrickjaja/claude-cowork-service/main/scripts/install.sh | bash
#   curl -fsSL .../install.sh | bash -s -- --user        # install to ~/.local/bin (no root)
#   curl -fsSL .../install.sh | bash -s -- --uninstall    # remove everything
#
set -euo pipefail

REPO="patrickjaja/claude-cowork-service"
BINARY_NAME="cowork-svc-linux"
SERVICE_NAME="claude-cowork"
DOWNLOAD_NAME=""  # set by arch detection below

# Colors (disabled if not a terminal)
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    NC='\033[0m'
else
    RED='' GREEN='' YELLOW='' BLUE='' NC=''
fi

info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
die()   { error "$@"; exit 1; }

# --- Parse arguments ---

MODE="install"
USER_INSTALL=false

for arg in "$@"; do
    case "$arg" in
        --user)      USER_INSTALL=true ;;
        --uninstall) MODE="uninstall" ;;
        --help|-h)
            echo "Usage: $0 [--user] [--uninstall]"
            echo ""
            echo "  --user       Install to ~/.local/bin (no root required)"
            echo "  --uninstall  Remove binary and systemd service"
            exit 0
            ;;
        *) die "Unknown argument: $arg" ;;
    esac
done

# --- Determine paths ---

if [ "$USER_INSTALL" = true ]; then
    INSTALL_DIR="$HOME/.local/bin"
else
    INSTALL_DIR="/usr/local/bin"
fi

BINARY_PATH="$INSTALL_DIR/$BINARY_NAME"
SERVICE_DIR="$HOME/.config/systemd/user"
SERVICE_FILE="$SERVICE_DIR/$SERVICE_NAME.service"

# --- Uninstall ---

do_uninstall() {
    info "Uninstalling $SERVICE_NAME..."

    # Stop and disable service
    if systemctl --user is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
        info "Stopping $SERVICE_NAME service..."
        systemctl --user stop "$SERVICE_NAME"
    fi
    if systemctl --user is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
        info "Disabling $SERVICE_NAME service..."
        systemctl --user disable "$SERVICE_NAME"
    fi

    # Remove service file
    if [ -f "$SERVICE_FILE" ]; then
        rm -f "$SERVICE_FILE"
        systemctl --user daemon-reload
        ok "Removed $SERVICE_FILE"
    fi

    # Remove binary
    if [ -f "$BINARY_PATH" ]; then
        if [ -w "$BINARY_PATH" ] || [ -w "$(dirname "$BINARY_PATH")" ]; then
            rm -f "$BINARY_PATH"
        else
            sudo rm -f "$BINARY_PATH"
        fi
        ok "Removed $BINARY_PATH"
    fi

    # Also check the other common location
    for path in /usr/local/bin/$BINARY_NAME "$HOME/.local/bin/$BINARY_NAME"; do
        if [ -f "$path" ] && [ "$path" != "$BINARY_PATH" ]; then
            warn "Found binary also at $path — remove manually if unwanted"
        fi
    done

    ok "Uninstall complete."
}

if [ "$MODE" = "uninstall" ]; then
    do_uninstall
    exit 0
fi

# --- Pre-flight checks ---

ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  DOWNLOAD_NAME="cowork-svc-linux" ;;
    aarch64) DOWNLOAD_NAME="cowork-svc-linux-arm64" ;;
    *) die "Unsupported architecture: $ARCH (supported: x86_64, aarch64)" ;;
esac
info "Detected architecture: $ARCH (downloading $DOWNLOAD_NAME)"

command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found)"

# Find a download tool
FETCH=""
if command -v curl >/dev/null 2>&1; then
    FETCH="curl"
elif command -v wget >/dev/null 2>&1; then
    FETCH="wget"
else
    die "curl or wget is required"
fi

# --- Determine latest version ---

info "Fetching latest release from $REPO..."

if [ "$FETCH" = "curl" ]; then
    RELEASE_URL=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
        "https://github.com/$REPO/releases/latest" 2>/dev/null) || \
        die "Failed to fetch latest release info"
else
    RELEASE_URL=$(wget -q --max-redirect=0 -O /dev/null \
        "https://github.com/$REPO/releases/latest" 2>&1 | \
        grep -oP 'Location: \K\S+' || true)
    [ -n "$RELEASE_URL" ] || die "Failed to fetch latest release info"
fi

VERSION="${RELEASE_URL##*/}"
[ -n "$VERSION" ] || die "Could not determine latest version"
info "Latest version: $VERSION"

DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$DOWNLOAD_NAME"

# --- Download binary ---

TMPDIR_INSTALL="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_INSTALL"' EXIT

TMPFILE="$TMPDIR_INSTALL/$BINARY_NAME"

info "Downloading $DOWNLOAD_NAME $VERSION..."
if [ "$FETCH" = "curl" ]; then
    curl -fSL --progress-bar -o "$TMPFILE" "$DOWNLOAD_URL" || die "Download failed"
else
    wget -q --show-progress -O "$TMPFILE" "$DOWNLOAD_URL" || die "Download failed"
fi

chmod +x "$TMPFILE"
ok "Downloaded $DOWNLOAD_NAME as $BINARY_NAME"

# --- Install binary ---

mkdir -p "$INSTALL_DIR"

if [ -w "$INSTALL_DIR" ]; then
    mv "$TMPFILE" "$BINARY_PATH"
else
    info "Installing to $INSTALL_DIR (requires sudo)..."
    sudo mv "$TMPFILE" "$BINARY_PATH"
fi

ok "Installed $BINARY_PATH"

# --- Create systemd user service ---

mkdir -p "$SERVICE_DIR"

cat > "$SERVICE_FILE" << EOF
[Unit]
Description=Claude Cowork Service (native Linux backend)
After=default.target

[Service]
ExecStart=$BINARY_PATH
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF

ok "Created $SERVICE_FILE"

# --- Enable and start ---

systemctl --user daemon-reload
systemctl --user enable "$SERVICE_NAME"
systemctl --user start "$SERVICE_NAME"

ok "Service $SERVICE_NAME enabled and started"

# --- Verify ---

sleep 1
if systemctl --user is-active --quiet "$SERVICE_NAME"; then
    ok "Installation complete! $SERVICE_NAME is running."
else
    warn "Service started but may not be active yet. Check with:"
    echo "  systemctl --user status $SERVICE_NAME"
fi

echo ""
info "To check status:  systemctl --user status $SERVICE_NAME"
info "To view logs:     journalctl --user -u $SERVICE_NAME -f"
info "To uninstall:     curl -fsSL https://raw.githubusercontent.com/$REPO/main/scripts/install.sh | bash -s -- --uninstall"
