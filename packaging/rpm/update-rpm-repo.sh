#!/bin/bash
# update-rpm-repo.sh — Build RPM repository metadata from .rpm files
#
# Usage: update-rpm-repo.sh <rpm_file> <repo_dir> <gpg_key_id> [arch]
#
# 1. Copies new .rpm into repo_dir/rpm/<arch>/
# 2. Prunes old versions per arch (keeps latest 2)
# 3. Runs createrepo_c to generate repodata/ (scans all arches)
# 4. GPG-signs repodata/repomd.xml
#
# arch defaults to "x86_64". Pass "aarch64" for ARM64 .rpms.

set -euo pipefail

RPM_FILE="$1"
REPO_DIR="$2"
GPG_KEY_ID="$3"
ARCH="${4:-x86_64}"

if [ ! -f "$RPM_FILE" ]; then
  echo "ERROR: .rpm file not found: $RPM_FILE"
  exit 1
fi

echo "=== Updating RPM repository ($ARCH) ==="
echo "  .rpm file:  $RPM_FILE"
echo "  Repo dir:   $REPO_DIR"
echo "  Arch:       $ARCH"
echo "  GPG key:    $GPG_KEY_ID"

# Create directory structure for this arch
mkdir -p "$REPO_DIR/rpm/$ARCH"

# Copy new .rpm
cp "$RPM_FILE" "$REPO_DIR/rpm/$ARCH/"
echo "Copied $(basename "$RPM_FILE") to rpm/$ARCH/"

# Prune old versions for this arch — keep latest 2
cd "$REPO_DIR/rpm/$ARCH"
# shellcheck disable=SC2012
ls -t *.rpm 2>/dev/null | tail -n +3 | xargs -r rm -f
KEPT=$(ls -1 *.rpm 2>/dev/null | wc -l)
echo "Kept $KEPT .rpm file(s) after pruning ($ARCH)"

# Generate repository metadata (scans all arch subdirectories)
cd "$REPO_DIR/rpm"
createrepo_c --update .
echo "Generated repodata/ (multi-arch)"

# GPG sign repomd.xml
rm -f repodata/repomd.xml.asc
gpg --batch --yes --default-key "$GPG_KEY_ID" --detach-sign --armor -o repodata/repomd.xml.asc repodata/repomd.xml
echo "Signed repodata/repomd.xml"

echo "=== RPM repository updated ==="
