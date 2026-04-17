package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VFS guest layout (mirrors cowork-vm-service.js constants).
const (
	VFSShareMountTag     = "claudeshared"
	VFSGuestMount        = "/mnt/.virtiofs-root"
	VFSGuestSharedPrefix = VFSGuestMount + "/shared"
	VsockGuestPort       = 51234 // 0xC822 — sdk-daemon's listening port
)

// hostAbsFromShared converts a guest-reported relative path (host absolute
// minus the leading slash, e.g. "home/ralph/.config/Claude/foo") into an
// absolute host path, enforcing that it lies under $HOME.
// sdk-daemon feeds these same strings through its own
// /mnt/.virtiofs-root/shared/ resolution, so we use them unchanged as the
// relpath under our staging dir.
func hostAbsFromShared(relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("relPath required")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("expected shared-relative path, got absolute: %s", relPath)
	}
	abs := filepath.Clean("/" + relPath)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home: %w", err)
	}
	if abs != home && !strings.HasPrefix(abs, home+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside home: %s", abs)
	}
	return abs, nil
}
