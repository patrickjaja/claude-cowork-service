#!/bin/bash
#
# Local build script for claude-cowork-service
#
# Builds everything from source (Go binary + sandbox-runtime) and produces
# an installable Arch Linux .pkg.tar.zst package.
#
# Usage: ./scripts/build-local.sh [--install] [--skip-srt]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

INSTALL_AFTER_BUILD=false
SKIP_SRT=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --install|-i)
            INSTALL_AFTER_BUILD=true
            shift
            ;;
        --skip-srt)
            SKIP_SRT=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --install, -i    Install the package after building"
            echo "  --skip-srt       Skip sandbox-runtime build (reuse existing srt/ binaries)"
            echo "  --help, -h       Show this help message"
            echo ""
            echo "This script:"
            echo "  1. Checks build dependencies (go, bun, makepkg)"
            echo "  2. Builds the Go binary (cowork-svc-linux)"
            echo "  3. Builds sandbox-runtime executables (srt-cowork)"
            echo "  4. Packages everything into a .pkg.tar.zst"
            echo "  5. Optionally installs it"
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            echo "Usage: $0 [--install] [--skip-srt]"
            exit 1
            ;;
    esac
done

# Check build dependencies
log_info "Checking build dependencies..."
MISSING_DEPS=()
for dep in go makepkg; do
    if ! command -v "$dep" &>/dev/null; then
        MISSING_DEPS+=("$dep")
    fi
done
if [ "$SKIP_SRT" = false ] && ! command -v bun &>/dev/null; then
    MISSING_DEPS+=("bun")
fi

if [ ${#MISSING_DEPS[@]} -ne 0 ]; then
    log_error "Missing dependencies: ${MISSING_DEPS[*]}"
    echo "  go:      sudo pacman -S go"
    echo "  bun:     curl -fsSL https://bun.sh/install | bash"
    echo "  makepkg: included in base-devel (sudo pacman -S base-devel)"
    exit 1
fi

cd "$REPO_ROOT"

# Detect version from git (sanitize for pacman: no hyphens allowed in pkgver)
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION="${VERSION#v}"
VERSION="${VERSION//-/.}"
log_info "Version: $VERSION"

# Detect architecture
UNAME_M=$(uname -m)
case "$UNAME_M" in
    x86_64)  TARGET_ARCH="x86_64"; SRT_ARCH="amd64" ;;
    aarch64) TARGET_ARCH="aarch64"; SRT_ARCH="arm64" ;;
    *) log_error "Unsupported architecture: $UNAME_M"; exit 1 ;;
esac
log_info "Architecture: $TARGET_ARCH"

# Build Go binary
log_info "Building cowork-svc-linux..."
make build
BINARY="$REPO_ROOT/cowork-svc-linux"

if [ ! -f "$BINARY" ]; then
    log_error "Go build failed - binary not found"
    exit 1
fi
log_info "Binary built: $(du -h "$BINARY" | cut -f1)"

# Build sandbox-runtime
SRT_BINARY="$REPO_ROOT/srt/srt-linux-$SRT_ARCH"
if [ "$SKIP_SRT" = true ]; then
    if [ ! -f "$SRT_BINARY" ]; then
        log_error "SRT binary not found at $SRT_BINARY (cannot --skip-srt without a previous build)"
        exit 1
    fi
    log_info "Reusing existing SRT binary: $(du -h "$SRT_BINARY" | cut -f1)"
else
    log_info "Building sandbox-runtime (this may take a minute)..."
    make build-srt
    if [ ! -f "$SRT_BINARY" ]; then
        log_error "SRT build failed - binary not found"
        exit 1
    fi
    log_info "SRT binary built: $(du -h "$SRT_BINARY" | cut -f1)"
fi

# Build the pacman package
log_info "Building Arch package..."
INSTALL_FLAG=""
if [ "$INSTALL_AFTER_BUILD" = true ]; then
    INSTALL_FLAG="--install"
fi
"$REPO_ROOT/packaging/arch/build-pkg.sh" $INSTALL_FLAG "$BINARY" "$VERSION" "$TARGET_ARCH"

echo ""
log_info "Build complete!"
