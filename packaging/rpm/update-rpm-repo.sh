#!/bin/bash
# update-rpm-repo.sh — Sign RPM and build repository metadata
#
# Usage: update-rpm-repo.sh <rpm_file> <repo_dir> <gpg_key_id> [arch]
#
# IMPORTANT: This script must run inside a Fedora/RHEL container (or host)
# where rpm-sign and createrepo_c are native packages. Do NOT run on Ubuntu.
#
# 1. Auto-detects arch from .rpm filename (or uses 4th arg)
# 2. Copies new .rpm into repo_dir/rpm/<arch>/
# 3. GPG-signs the RPM package (rpmsign --addsign)
# 4. Verifies the signature (rpm -K)
# 5. Prunes old versions per arch (keeps latest 2)
# 6. Runs createrepo_c to generate repodata/
# 7. GPG-signs repodata/repomd.xml (detached armored signature)

set -euo pipefail

RPM_FILE="$1"
REPO_DIR="$2"
GPG_KEY_ID="$3"
ARCH="${4:-}"

if [ ! -f "$RPM_FILE" ]; then
  echo "ERROR: .rpm file not found: $RPM_FILE"
  exit 1
fi

RPM_BASENAME=$(basename "$RPM_FILE")

# Auto-detect architecture from .rpm filename if not provided
if [ -z "$ARCH" ]; then
  if [[ "$RPM_BASENAME" =~ \.(x86_64|aarch64|noarch)\.rpm$ ]]; then
      ARCH="${BASH_REMATCH[1]}"
  else
      echo "WARNING: Could not detect arch from filename, defaulting to x86_64"
      ARCH="x86_64"
  fi
fi

echo "=== Updating RPM repository ==="
echo "  .rpm file:  $RPM_FILE"
echo "  Arch:       $ARCH"
echo "  Repo dir:   $REPO_DIR"
echo "  GPG key:    $GPG_KEY_ID"

# Create directory structure for this architecture
mkdir -p "$REPO_DIR/rpm/$ARCH"

# Copy new .rpm
cp "$RPM_FILE" "$REPO_DIR/rpm/$ARCH/"
echo "Copied $RPM_BASENAME to rpm/$ARCH/"

# Sign the RPM package (gpgcheck=1 in repo config requires this)
cat > ~/.rpmmacros <<MACROS
%_gpg_name $GPG_KEY_ID
%__gpg_sign_cmd %{__gpg} --batch --verbose --no-armor --no-secmem-warning -u "%{_gpg_name}" -sbo %{__signature_filename} --digest-algo sha256 %{__plaintext_filename}
MACROS
rpmsign --addsign "$REPO_DIR/rpm/$ARCH/$RPM_BASENAME"

# Verify signature with rpm -K (works natively on Fedora)
gpg --armor --export "$GPG_KEY_ID" > /tmp/rpm-verify-key.asc
rpm --import /tmp/rpm-verify-key.asc
rpm -K "$REPO_DIR/rpm/$ARCH/$RPM_BASENAME" | grep -qi "signatures ok" || {
  echo "ERROR: RPM signature verification failed for $RPM_BASENAME"
  rpm -K "$REPO_DIR/rpm/$ARCH/$RPM_BASENAME"
  exit 1
}
echo "Signed and verified $RPM_BASENAME"

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
