#!/usr/bin/env bash
#
# Extract all binaries from the latest Claude Desktop Windows installer
#
# Downloads the installer, extracts the nupkg, and copies all files
# from the cowork-svc.exe directory level into bin/ for reverse engineering.
#
# Usage: ./scripts/extract-cowork-svc.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

OUTPUT_DIR="$PROJECT_DIR/bin"
VERSION_FILE="$OUTPUT_DIR/.version"

VERSION_API="https://downloads.claude.ai/releases/win32/x64/.latest"
USER_AGENT="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

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

# --- Check dependencies ---

MISSING_DEPS=()
for dep in 7z python3; do
    if ! command -v "$dep" &>/dev/null; then
        MISSING_DEPS+=("$dep")
    fi
done

if [ ${#MISSING_DEPS[@]} -ne 0 ]; then
    die "Missing dependencies: ${MISSING_DEPS[*]}
  Arch:   sudo pacman -S p7zip python
  Debian: sudo apt install p7zip-full python3"
fi

# Find a download tool
FETCH=""
if command -v curl >/dev/null 2>&1; then
    FETCH="curl"
elif command -v wget >/dev/null 2>&1; then
    FETCH="wget"
else
    die "curl or wget is required"
fi

# --- Query version API ---

info "Querying Claude Desktop version API..."

if [ "$FETCH" = "curl" ]; then
    LATEST_JSON=$(curl -fsSL -A "$USER_AGENT" "$VERSION_API") || die "Failed to query version API"
else
    LATEST_JSON=$(wget -q -O - -U "$USER_AGENT" "$VERSION_API") || die "Failed to query version API"
fi

[ -n "$LATEST_JSON" ] || die "Empty response from version API"

LATEST_VERSION=$(echo "$LATEST_JSON" | python3 -c "import sys,json; print(json.load(sys.stdin)['version'])")
LATEST_HASH=$(echo "$LATEST_JSON" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")

info "Latest version: $LATEST_VERSION"

# --- Idempotency check ---

if [ -d "$OUTPUT_DIR" ] && [ -f "$VERSION_FILE" ]; then
    CURRENT_VERSION=$(cat "$VERSION_FILE")
    if [ "$CURRENT_VERSION" = "$LATEST_VERSION" ]; then
        ok "bin/ is already at version $LATEST_VERSION — nothing to do."
        exit 0
    fi
    info "Upgrading from $CURRENT_VERSION to $LATEST_VERSION"
fi

# --- Download installer ---

DOWNLOAD_URL="https://downloads.claude.ai/releases/win32/x64/${LATEST_VERSION}/Claude-${LATEST_HASH}.exe"

TMPDIR_WORK="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_WORK"' EXIT

info "Downloading Claude Desktop installer (~146 MB)..."
info "URL: $DOWNLOAD_URL"

EXE_FILE="$TMPDIR_WORK/Claude-Setup-x64.exe"

if [ "$FETCH" = "curl" ]; then
    curl -fSL --progress-bar -A "$USER_AGENT" -o "$EXE_FILE" "$DOWNLOAD_URL" || die "Download failed"
else
    wget --show-progress -O "$EXE_FILE" -U "$USER_AGENT" "$DOWNLOAD_URL" || die "Download failed"
fi

[ -s "$EXE_FILE" ] || die "Downloaded file is empty"
ok "Download complete"

# --- Extract installer ---

info "Extracting installer..."
7z x -y "$EXE_FILE" -o"$TMPDIR_WORK/extract" >/dev/null 2>&1

# --- Extract nupkg ---

info "Extracting nupkg..."
NUPKG=$(find "$TMPDIR_WORK/extract" -maxdepth 1 -name "AnthropicClaude-*.nupkg" | head -1)
[ -n "$NUPKG" ] || die "No AnthropicClaude nupkg found in installer"

7z x -y "$NUPKG" -o"$TMPDIR_WORK/nupkg" >/dev/null 2>&1

# --- Find cowork-svc.exe and copy all files from its directory ---

COWORK_SVC="$TMPDIR_WORK/nupkg/lib/net45/cowork-svc.exe"

if [ ! -f "$COWORK_SVC" ]; then
    info "cowork-svc.exe not at expected path, searching..."
    COWORK_SVC=$(find "$TMPDIR_WORK/nupkg" -name "cowork-svc.exe" -type f | head -1)
    [ -n "$COWORK_SVC" ] || die "cowork-svc.exe not found in nupkg"
fi

BIN_SOURCE_DIR="$(dirname "$COWORK_SVC")"

info "Found binaries in: $BIN_SOURCE_DIR"
info "Files at this level:"
find "$BIN_SOURCE_DIR" -maxdepth 1 -type f -exec ls -lh {} + 2>/dev/null | while read -r line; do
    info "  $line"
done || true

# Create output dir and copy all files from the same level
mkdir -p "$OUTPUT_DIR"
rm -rf "$OUTPUT_DIR"/*

# Copy all files (not subdirectories) from the cowork-svc.exe directory
find "$BIN_SOURCE_DIR" -maxdepth 1 -type f -exec cp {} "$OUTPUT_DIR/" \;

FILE_COUNT=$(find "$OUTPUT_DIR" -maxdepth 1 -type f | wc -l)
echo "$LATEST_VERSION" > "$VERSION_FILE"
echo "$LATEST_VERSION" > "$PROJECT_DIR/.upstream-version"

ok "Extracted $FILE_COUNT files (version $LATEST_VERSION) to bin/"
info "Contents:"
ls -lh "$OUTPUT_DIR" | tail -n +2
