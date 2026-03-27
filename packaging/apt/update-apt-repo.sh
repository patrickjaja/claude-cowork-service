#!/bin/bash
# update-apt-repo.sh — Build APT repository metadata from .deb files
#
# Usage: update-apt-repo.sh <deb_file> <repo_dir> <gpg_key_id> [arch]
#
# 1. Copies new .deb into repo_dir/deb/<arch>/
# 2. Prunes old versions per arch (keeps latest 2)
# 3. Generates Packages, Packages.gz, Release (multi-arch)
# 4. GPG-signs Release (Release.gpg + InRelease)
#
# arch defaults to "amd64". Pass "arm64" for ARM64 .debs.

set -euo pipefail

DEB_FILE="$1"
REPO_DIR="$2"
GPG_KEY_ID="$3"
ARCH="${4:-amd64}"

if [ ! -f "$DEB_FILE" ]; then
  echo "ERROR: .deb file not found: $DEB_FILE"
  exit 1
fi

echo "=== Updating APT repository ($ARCH) ==="
echo "  .deb file:  $DEB_FILE"
echo "  Repo dir:   $REPO_DIR"
echo "  Arch:       $ARCH"
echo "  GPG key:    $GPG_KEY_ID"

# Create directory structure for this arch
mkdir -p "$REPO_DIR/deb/$ARCH"

# Copy new .deb
cp "$DEB_FILE" "$REPO_DIR/deb/$ARCH/"
echo "Copied $(basename "$DEB_FILE") to deb/$ARCH/"

# Prune old versions for this arch — keep latest 2
cd "$REPO_DIR/deb/$ARCH"
# shellcheck disable=SC2012
ls -t *.deb 2>/dev/null | tail -n +3 | xargs -r rm -f
KEPT=$(ls -1 *.deb 2>/dev/null | wc -l)
echo "Kept $KEPT .deb file(s) after pruning ($ARCH)"

# Generate Packages index — scan all arch directories
cd "$REPO_DIR/deb"
> Packages  # truncate
for arch_dir in amd64 arm64; do
  if [ -d "$arch_dir" ] && ls "$arch_dir"/*.deb >/dev/null 2>&1; then
    dpkg-scanpackages --arch "$arch_dir" "$arch_dir/" >> Packages
  fi
done
gzip -9c Packages > Packages.gz
echo "Generated Packages index ($(wc -l < Packages) lines, multi-arch)"

# Generate Release file
apt-ftparchive release . > Release
echo "Generated Release file"

# GPG sign
rm -f Release.gpg InRelease
gpg --batch --yes --default-key "$GPG_KEY_ID" --detach-sign --armor -o Release.gpg Release
gpg --batch --yes --default-key "$GPG_KEY_ID" --clearsign -o InRelease Release
echo "Signed Release (Release.gpg + InRelease)"

echo "=== APT repository updated ==="
