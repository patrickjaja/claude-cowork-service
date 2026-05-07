#!/usr/bin/env bash
#
# Extract VM bundle from Claude Desktop and download the VM files
#
# Downloads the Claude Desktop Windows MSIX package, extracts app.asar,
# parses the embedded VM bundle config (sha + file list), and downloads
# the VM bundle files (vmlinuz, initrd, rootfs) for investigation.
#
# The VM bundle download URL pattern (from app.asar JS):
#   https://downloads.claude.ai/vms/linux/<sha>/<arch>/<filename>.zst
#
# Usage: ./scripts/extract-vm-bundle.sh [--arch x64|arm64]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

VM_OUTPUT_DIR="$PROJECT_DIR/vm-bundle"
VERSION_FILE="$VM_OUTPUT_DIR/.version"

VERSION_API="https://downloads.claude.ai/releases/win32/x64/.latest"
VM_BASE_URL="https://downloads.claude.ai/vms/linux"
USER_AGENT="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

# Default to x64; override with --arch arm64
VM_ARCH="x64"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --arch) VM_ARCH="$2"; shift 2 ;;
        *) echo "Usage: $0 [--arch x64|arm64]"; exit 1 ;;
    esac
done

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

# Check for asar extraction capability
HAS_ASAR=false
if command -v npx &>/dev/null; then
    HAS_ASAR=true
elif command -v asar &>/dev/null; then
    HAS_ASAR=true
fi

if [ ${#MISSING_DEPS[@]} -ne 0 ]; then
    die "Missing dependencies: ${MISSING_DEPS[*]}
  Arch:   sudo pacman -S p7zip python
  Debian: sudo apt install p7zip-full python3"
fi

if [ "$HAS_ASAR" = false ]; then
    warn "No asar tool found. Will attempt extraction with 7z (may be incomplete)."
    warn "For full extraction: npm install -g @electron/asar"
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

fetch_url() {
    local url="$1" output="$2"
    if [ "$FETCH" = "curl" ]; then
        curl -fSL --progress-bar -A "$USER_AGENT" -o "$output" "$url"
    else
        wget --show-progress -O "$output" -U "$USER_AGENT" "$url"
    fi
}

fetch_silent() {
    local url="$1"
    if [ "$FETCH" = "curl" ]; then
        curl -fsSL -A "$USER_AGENT" "$url"
    else
        wget -q -O - -U "$USER_AGENT" "$url"
    fi
}

# --- Query version API ---

info "Querying Claude Desktop version API..."

LATEST_JSON=$(fetch_silent "$VERSION_API") || die "Failed to query version API"
[ -n "$LATEST_JSON" ] || die "Empty response from version API"

LATEST_VERSION=$(echo "$LATEST_JSON" | python3 -c "import sys,json; print(json.load(sys.stdin)['version'])")
LATEST_HASH=$(echo "$LATEST_JSON" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")

info "Latest version: $LATEST_VERSION"
info "Target arch: $VM_ARCH"

# --- Idempotency check ---

if [ -d "$VM_OUTPUT_DIR" ] && [ -f "$VERSION_FILE" ]; then
    CURRENT_VERSION=$(cat "$VERSION_FILE")
    if [ "$CURRENT_VERSION" = "$LATEST_VERSION" ]; then
        ok "vm-bundle/ is already at version $LATEST_VERSION - nothing to do."
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
fetch_url "$DOWNLOAD_URL" "$MSIX_FILE" || die "Download failed"
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

# --- Find app.asar in MSIX layout ---

ASAR_FILE="$TMPDIR_WORK/extract/app/resources/app.asar"
[ -f "$ASAR_FILE" ] || die "app.asar not found at app/resources/ - is this a valid Claude MSIX?"
ok "Found app.asar: $ASAR_FILE"

ASAR_DIR="$TMPDIR_WORK/asar-extracted"
info "Extracting app.asar..."

if command -v npx &>/dev/null; then
    npx --yes @electron/asar extract "$ASAR_FILE" "$ASAR_DIR" 2>/dev/null || {
        warn "npx asar extract failed, trying 7z..."
        7z x -y "$ASAR_FILE" -o"$ASAR_DIR" >/dev/null 2>&1 || die "Failed to extract app.asar"
    }
elif command -v asar &>/dev/null; then
    asar extract "$ASAR_FILE" "$ASAR_DIR" || {
        warn "asar extract failed, trying 7z..."
        7z x -y "$ASAR_FILE" -o"$ASAR_DIR" >/dev/null 2>&1 || die "Failed to extract app.asar"
    }
else
    7z x -y "$ASAR_FILE" -o"$ASAR_DIR" >/dev/null 2>&1 || die "Failed to extract app.asar"
fi
ok "app.asar extracted"

# --- Parse VM bundle config from JS ---
#
# The app embeds a config object like:
#   const qn = {
#     sha: "fb30784...",
#     files: { win32: { x64: [{name:"rootfs.vhdx",...}, ...] }, ... }
#   }
#
# Download URL pattern: https://downloads.claude.ai/vms/linux/<sha>/<arch>/<name>.zst
#

info "Parsing VM bundle config from app JS..."

INDEX_JS=$(find "$ASAR_DIR" -name "index.js" -path "*/.vite/build/*" | head -1)
[ -n "$INDEX_JS" ] || die "index.js not found in extracted asar"

# Extract the config object using python for reliable JSON parsing
VM_CONFIG=$(INDEX_JS_PATH="$INDEX_JS" python3 -c "
import os, re, json

# Read the index.js file
with open(os.environ['INDEX_JS_PATH'], 'r', errors='replace') as f:
    content = f.read()

# Find the config object: {sha:\"...\",files:{...}}
match = re.search(r'\{sha:\"([a-f0-9]{40})\",files:\{', content)
if not match:
    print('ERROR: Could not find VM bundle config in index.js', file=__import__('sys').stderr)
    raise SystemExit(1)

# Extract the full object by finding balanced braces
start = match.start()
depth = 0
end = start
for i in range(start, min(start + 5000, len(content))):
    if content[i] == '{': depth += 1
    elif content[i] == '}': depth -= 1
    if depth == 0:
        end = i + 1
        break

obj_str = content[start:end]

# Convert to valid JSON by quoting unquoted keys
obj_str = re.sub(r'(?<=[{,])(\w+):', r'\"\1\":', obj_str)

data = json.loads(obj_str)
print(json.dumps(data))
") || die "Failed to parse VM bundle config"

VM_SHA=$(echo "$VM_CONFIG" | python3 -c "import sys,json; print(json.load(sys.stdin)['sha'])")

info "VM bundle SHA: $VM_SHA"

# Get the file list for win32 (which has the full set: rootfs.vhdx, vmlinuz, initrd)
VM_FILES=$(echo "$VM_CONFIG" | VM_ARCH_PY="$VM_ARCH" python3 -c "
import sys, json, os
config = json.load(sys.stdin)
arch = os.environ['VM_ARCH_PY']
# Prefer win32 files (has vmlinuz, initrd, rootfs.vhdx)
# darwin only has rootfs.img
files = config['files'].get('win32', {}).get(arch, [])
if not files:
    files = config['files'].get('darwin', {}).get(arch, [])
for f in files:
    print(f'{f[\"name\"]}\t{f[\"checksum\"]}')
")

if [ -z "$VM_FILES" ]; then
    die "No VM files found for arch $VM_ARCH"
fi

info "VM bundle files for win32/$VM_ARCH:"
echo "$VM_FILES" | while IFS=$'\t' read -r name checksum; do
    info "  $name (sha256: ${checksum:0:16}...)"
done

# --- Download VM bundle files ---

mkdir -p "$VM_OUTPUT_DIR"

# URL pattern from JS: https://downloads.claude.ai/vms/linux/<arch>/<sha>/<filename>.zst
info ""
info "Downloading VM bundle files..."
info "Base URL: $VM_BASE_URL/$VM_ARCH/$VM_SHA/"

echo "$VM_FILES" | while IFS=$'\t' read -r name checksum; do
    ZST_NAME="${name}.zst"
    FILE_URL="$VM_BASE_URL/$VM_ARCH/$VM_SHA/$ZST_NAME"
    OUTPUT_FILE="$VM_OUTPUT_DIR/$ZST_NAME"

    if [ -f "$OUTPUT_FILE" ]; then
        info "  $ZST_NAME already exists, skipping"
        continue
    fi

    info "  Downloading $ZST_NAME..."
    info "  URL: $FILE_URL"

    if ! fetch_url "$FILE_URL" "$OUTPUT_FILE"; then
        warn "  Failed to download $ZST_NAME"
        rm -f "$OUTPUT_FILE"
        # Try without .zst suffix (some files might not be compressed)
        FILE_URL_RAW="$VM_BASE_URL/$VM_ARCH/$VM_SHA/$name"
        info "  Trying without .zst: $FILE_URL_RAW"
        if ! fetch_url "$FILE_URL_RAW" "$VM_OUTPUT_DIR/$name"; then
            error "  Failed to download $name"
            rm -f "$VM_OUTPUT_DIR/$name"
        else
            ok "  Downloaded $name"
        fi
    else
        ok "  Downloaded $ZST_NAME"
    fi
done

# --- Save extracted asar content for investigation ---

if [ -d "$ASAR_DIR" ]; then
    info ""
    info "Saving extracted app.asar contents for investigation..."
    rm -rf "$VM_OUTPUT_DIR/app-asar-extracted"
    cp -r "$ASAR_DIR" "$VM_OUTPUT_DIR/app-asar-extracted"
    ok "Saved app.asar contents to vm-bundle/app-asar-extracted/"
fi

# --- Save the parsed config ---

echo "$VM_CONFIG" | python3 -m json.tool > "$VM_OUTPUT_DIR/vm-bundle-config.json"
ok "Saved parsed config to vm-bundle/vm-bundle-config.json"

echo "$LATEST_VERSION" > "$VERSION_FILE"

# --- Summary ---

info ""
ok "VM bundle extraction complete (version $LATEST_VERSION)"
info ""
info "Bundle SHA:  $VM_SHA"
info "Files saved to: $VM_OUTPUT_DIR/"
ls -lh "$VM_OUTPUT_DIR"/*.zst "$VM_OUTPUT_DIR"/*.vhdx "$VM_OUTPUT_DIR"/vmlinuz "$VM_OUTPUT_DIR"/initrd 2>/dev/null | while read -r line; do
    info "  $line"
done
info ""
info "To decompress (requires zstd):"
info "  cd $VM_OUTPUT_DIR"
info "  zstd -d rootfs.vhdx.zst"
info "  zstd -d vmlinuz.zst"
info "  zstd -d initrd.zst"
info ""
info "To convert VHDX to qcow2 (requires qemu-img):"
info "  qemu-img convert -f vhdx -O qcow2 rootfs.vhdx rootfs.qcow2"
