#!/usr/bin/env bash
#
# Extract all binaries from the latest Claude Desktop Windows MSIX package
#
# Downloads the MSIX, extracts it (flat ZIP), URL-decodes filenames,
# and copies all flat files from app/resources/ into bin/ for reverse
# engineering.
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
        ok "bin/ is already at version $LATEST_VERSION - nothing to do."
        exit 0
    fi
    info "Upgrading from $CURRENT_VERSION to $LATEST_VERSION"
fi

# --- Download MSIX package ---

DOWNLOAD_URL="https://downloads.claude.ai/releases/win32/x64/${LATEST_VERSION}/Claude-${LATEST_HASH}.msix"

TMPDIR_WORK="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_WORK"' EXIT

info "Downloading Claude Desktop MSIX package..."
info "URL: $DOWNLOAD_URL"

MSIX_FILE="$TMPDIR_WORK/Claude.msix"

if [ "$FETCH" = "curl" ]; then
    curl -fSL --progress-bar -A "$USER_AGENT" -o "$MSIX_FILE" "$DOWNLOAD_URL" || die "Download failed"
else
    wget --show-progress -O "$MSIX_FILE" -U "$USER_AGENT" "$DOWNLOAD_URL" || die "Download failed"
fi

[ -s "$MSIX_FILE" ] || die "Downloaded file is empty"
ok "Download complete"

# --- Extract MSIX (flat ZIP with app/, assets/, AppxManifest.xml) ---

info "Extracting MSIX package..."
7z x -y "$MSIX_FILE" -o"$TMPDIR_WORK/extract" >/dev/null 2>&1

# --- URL-decode filenames (MSIX encodes @ as %40 etc.) ---

info "URL-decoding MSIX paths..."
python3 -c "
import os, urllib.parse
root = '$TMPDIR_WORK/extract'
for dirpath, dirnames, filenames in os.walk(root, topdown=False):
    for name in filenames + dirnames:
        decoded = urllib.parse.unquote(name)
        if decoded != name:
            os.rename(os.path.join(dirpath, name), os.path.join(dirpath, decoded))
"

# --- Copy flat files from app/resources/ into bin/ ---

BIN_SOURCE_DIR="$TMPDIR_WORK/extract/app/resources"
[ -d "$BIN_SOURCE_DIR" ] || die "app/resources/ not found in MSIX - is this a valid Claude package?"

COWORK_SVC="$BIN_SOURCE_DIR/cowork-svc.exe"
[ -f "$COWORK_SVC" ] || die "cowork-svc.exe not found at $BIN_SOURCE_DIR"

info "Found binaries in: $BIN_SOURCE_DIR"
info "Files at this level:"
find "$BIN_SOURCE_DIR" -maxdepth 1 -type f -exec ls -lh {} + 2>/dev/null | while read -r line; do
    info "  $line"
done || true

mkdir -p "$OUTPUT_DIR"
rm -rf "$OUTPUT_DIR"/*

# Copy only flat files (skip subdirectories like fonts/, ion-dist/, app.asar.unpacked/)
find "$BIN_SOURCE_DIR" -maxdepth 1 -type f -exec cp {} "$OUTPUT_DIR/" \;

FILE_COUNT=$(find "$OUTPUT_DIR" -maxdepth 1 -type f | wc -l)
echo "$LATEST_VERSION" > "$VERSION_FILE"
echo "$LATEST_VERSION" > "$PROJECT_DIR/.upstream-version"

ok "Extracted $FILE_COUNT files (version $LATEST_VERSION) to bin/"
info "Contents:"
ls -lh "$OUTPUT_DIR" | tail -n +2
