#!/bin/bash
# update-apt-repo.sh — Build APT repository metadata from .deb files
#
# Usage: update-apt-repo.sh <deb_file> <repo_dir> <gpg_key_id>
#
# 1. Copies new .deb into repo_dir/deb/amd64/
# 2. Prunes old versions (keeps latest 2)
# 3. Generates Packages, Packages.gz, Release
# 4. GPG-signs Release (Release.gpg + InRelease)

set -euo pipefail

DEB_FILE="$1"
REPO_DIR="$2"
GPG_KEY_ID="$3"

if [ ! -f "$DEB_FILE" ]; then
  echo "ERROR: .deb file not found: $DEB_FILE"
  exit 1
fi

echo "=== Updating APT repository ==="
echo "  .deb file:  $DEB_FILE"
echo "  Repo dir:   $REPO_DIR"
echo "  GPG key:    $GPG_KEY_ID"

# Create directory structure
mkdir -p "$REPO_DIR/deb/amd64"

# Copy new .deb
cp "$DEB_FILE" "$REPO_DIR/deb/amd64/"
echo "Copied $(basename "$DEB_FILE") to deb/amd64/"

# Prune old versions — keep latest 2
cd "$REPO_DIR/deb/amd64"
# shellcheck disable=SC2012
ls -t *.deb 2>/dev/null | tail -n +3 | xargs -r rm -f
KEPT=$(ls -1 *.deb 2>/dev/null | wc -l)
echo "Kept $KEPT .deb file(s) after pruning"

# Generate Packages index
cd "$REPO_DIR/deb"
dpkg-scanpackages --arch amd64 amd64/ > Packages
gzip -9c Packages > Packages.gz
echo "Generated Packages index ($(wc -l < Packages) lines)"

# Generate Release file
apt-ftparchive release . > Release
echo "Generated Release file"

# GPG sign
rm -f Release.gpg InRelease
gpg --batch --yes --default-key "$GPG_KEY_ID" --detach-sign --armor -o Release.gpg Release
gpg --batch --yes --default-key "$GPG_KEY_ID" --clearsign -o InRelease Release
echo "Signed Release (Release.gpg + InRelease)"

echo "=== APT repository updated ==="
