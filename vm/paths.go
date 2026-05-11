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

// canonicalizePathVM resolves symlinks in the longest existing prefix of path.
// Handles paths where leaf components don't yet exist on disk by walking up
// to the nearest existing ancestor and resolving from there.
func canonicalizePathVM(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	dir := filepath.Dir(path)
	if dir == path {
		return path
	}
	return filepath.Join(canonicalizePathVM(dir), filepath.Base(path))
}

// hostAbsFromShared converts a guest-reported relative path (host absolute
// minus the leading slash, e.g. "home/ralph/.config/Claude/foo") into an
// absolute host path, enforcing that it lies under $HOME.
// sdk-daemon feeds these same strings through its own
// /mnt/.virtiofs-root/shared/ resolution, so we use them unchanged as the
// relpath under our staging dir.
func hostAbsFromShared(relPath string) (string, error) {
	return hostAbsFromSharedWithHome(relPath, "")
}

// hostAbsFromSharedWithHome is the testable core of hostAbsFromShared.
// Pass an empty home to use os.UserHomeDir().
func hostAbsFromSharedWithHome(relPath, home string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("relPath required")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("expected shared-relative path, got absolute: %s", relPath)
	}
	abs := filepath.Clean("/" + relPath)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home: %w", err)
		}
	}
	sep := string(filepath.Separator)
	if abs == home || strings.HasPrefix(abs, home+sep) {
		return abs, nil
	}
	// Slow path: resolve symlinks to handle /home -> /var/home style layouts.
	absCanon := canonicalizePathVM(abs)
	homeCanon := canonicalizePathVM(home)
	if absCanon == homeCanon || strings.HasPrefix(absCanon, homeCanon+sep) {
		return absCanon, nil
	}
	return "", fmt.Errorf("path outside home: %s", abs)
}
