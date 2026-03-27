#!/bin/bash
# install-rpm.sh — Set up the Claude Cowork Service RPM repository
#
# Usage: curl -fsSL https://patrickjaja.github.io/claude-cowork-service/install-rpm.sh | sudo bash

set -euo pipefail

REPO_URL="https://patrickjaja.github.io/claude-cowork-service"
REPO_FILE="/etc/yum.repos.d/claude-cowork-service.repo"

# Check root
if [ "$(id -u)" -ne 0 ]; then
  echo "Error: This script must be run as root (use sudo)."
  exit 1
fi

echo "Setting up Claude Cowork Service RPM repository..."

# Import GPG key
rpm --import "$REPO_URL/gpg-key.asc"
echo "  GPG key imported"

# Add repository
cat > "$REPO_FILE" <<EOF
[claude-cowork-service]
name=Claude Cowork Service for Linux
baseurl=$REPO_URL/rpm/
enabled=1
gpgcheck=1
gpgkey=$REPO_URL/gpg-key.asc
EOF
echo "  Repository added to $REPO_FILE"

# Update package cache
dnf makecache --repo=claude-cowork-service
echo ""
echo "Done! Install Claude Cowork Service with:"
echo ""
echo "  sudo dnf install claude-cowork-service"
echo ""
echo "Future updates via: sudo dnf upgrade claude-cowork-service"
