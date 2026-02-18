#!/bin/bash
# build-deb.sh — Build .deb package for claude-cowork-service
#
# Usage: build-deb.sh <binary_path> <version>
#
# Creates claude-cowork-service_<version>_amd64.deb in the current directory.
# The package contains the static Go binary + systemd user service.

set -euo pipefail

BINARY="$1"
VERSION="$2"

if [ ! -f "$BINARY" ]; then
  echo "ERROR: Binary not found: $BINARY"
  exit 1
fi

PKG="claude-cowork-service"
ARCH="amd64"
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

# Install systemd service
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
install -m644 "$REPO_ROOT/dist/claude-cowork.service" "$BUILD_DIR/usr/lib/systemd/user/claude-cowork.service"

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
Recommends: systemd
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
