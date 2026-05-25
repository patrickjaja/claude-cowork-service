#!/bin/bash
# build-deb.sh — Build .deb package for claude-cowork-service
#
# Usage: build-deb.sh <binary_path> <version> [arch]
#
# Creates claude-cowork-service_<version>_<arch>.deb in the current directory.
# The package contains the Go binary, the matching sandbox-runtime `srt`
# binary, and the systemd user service.
#
# arch defaults to "amd64". Pass "arm64" for ARM64 builds.

set -euo pipefail

BINARY="$1"
VERSION="$2"
ARCH="${3:-amd64}"

if [ ! -f "$BINARY" ]; then
  echo "ERROR: Binary not found: $BINARY"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

case "$ARCH" in
  amd64|arm64) SRT_ARCH="$ARCH" ;;
  *) echo "ERROR: Unsupported arch for srt: $ARCH"; exit 1 ;;
esac
SRT_BINARY="${SRT_BINARY:-$REPO_ROOT/srt/srt-linux-$SRT_ARCH}"
if [ ! -f "$SRT_BINARY" ]; then
  echo "ERROR: SRT binary not found: $SRT_BINARY"
  echo "       Run: make build-srt"
  exit 1
fi

PKG="claude-cowork-service"
DEB_NAME="${PKG}_${VERSION}_${ARCH}.deb"
BUILD_DIR="$(mktemp -d)"

trap 'rm -rf "$BUILD_DIR"' EXIT

echo "=== Building ${DEB_NAME} ==="

# Create directory structure
mkdir -p "$BUILD_DIR/DEBIAN"
mkdir -p "$BUILD_DIR/usr/bin"
mkdir -p "$BUILD_DIR/usr/lib/systemd/user"

# Install binary
install -m755 "$BINARY" "$BUILD_DIR/usr/bin/cowork-svc-linux"
install -m755 "$SRT_BINARY" "$BUILD_DIR/usr/bin/srt-cowork"

# Install systemd service
install -m644 "$REPO_ROOT/claude-cowork.service" "$BUILD_DIR/usr/lib/systemd/user/claude-cowork.service"

# Create control file
cat > "$BUILD_DIR/DEBIAN/control" <<EOF
Package: ${PKG}
Version: ${VERSION}
Architecture: ${ARCH}
Maintainer: Patrick Jaja <patrick@jaja.dev>
Description: Native Linux backend for Claude Desktop's Cowork feature
 Reverse-engineered from Windows cowork-svc.exe. Implements the
 length-prefixed JSON-over-Unix-socket protocol that Claude Desktop
 expects, running commands directly on the host instead of in a VM.
Homepage: https://github.com/patrickjaja/claude-cowork-service
Section: utils
Priority: optional
Depends: systemd, bubblewrap, socat, ripgrep
Provides: claude-cowork-service
EOF

# Create postinst
cat > "$BUILD_DIR/DEBIAN/postinst" <<'EOF'
#!/bin/bash
set -e

echo ""
echo "claude-cowork-service installed successfully!"
echo ""
echo "Enable and start the service with:"
echo "  systemctl --user daemon-reload"
echo "  systemctl --user enable --now claude-cowork"
echo ""
EOF
chmod 755 "$BUILD_DIR/DEBIAN/postinst"

# Build .deb
dpkg-deb --build --root-owner-group "$BUILD_DIR" "$DEB_NAME"
echo "=== Built ${DEB_NAME} ($(du -h "$DEB_NAME" | cut -f1)) ==="
