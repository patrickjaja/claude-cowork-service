package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalizePathVM(t *testing.T) {
	t.Run("ExistingPath", func(t *testing.T) {
		dir := t.TempDir()
		got := canonicalizePathVM(dir)
		want, _ := filepath.EvalSymlinks(dir)
		if got != want {
			t.Errorf("canonicalizePathVM(%q) = %q, want %q", dir, got, want)
		}
	})

	t.Run("NonExistentLeaf", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "missing")
		got := canonicalizePathVM(path)
		want := filepath.Join(dir, "missing")
		if got != want {
			t.Errorf("canonicalizePathVM(%q) = %q, want %q", path, got, want)
		}
	})

	t.Run("SymlinkResolution", func(t *testing.T) {
		dir := t.TempDir()
		realDir := filepath.Join(dir, "real")
		linkDir := filepath.Join(dir, "link")
		if err := os.Mkdir(realDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realDir, linkDir); err != nil {
			t.Fatal(err)
		}
		got := canonicalizePathVM(filepath.Join(linkDir, "child"))
		want := filepath.Join(realDir, "child")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestHostAbsFromSharedWithHome(t *testing.T) {
	t.Run("EmptyRelPath", func(t *testing.T) {
		_, err := hostAbsFromSharedWithHome("", "/home/alice")
		if err == nil || !strings.Contains(err.Error(), "relPath required") {
			t.Errorf("expected relPath required error, got %v", err)
		}
	})

	t.Run("AbsolutePath", func(t *testing.T) {
		_, err := hostAbsFromSharedWithHome("/home/alice/.config", "/home/alice")
		if err == nil || !strings.Contains(err.Error(), "expected shared-relative") {
			t.Errorf("expected shared-relative error, got %v", err)
		}
	})

	t.Run("ValidPathUnderHome", func(t *testing.T) {
		got, err := hostAbsFromSharedWithHome("home/alice/.config/Claude", "/home/alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/home/alice/.config/Claude" {
			t.Errorf("got %q, want /home/alice/.config/Claude", got)
		}
	})

	t.Run("ExactHome", func(t *testing.T) {
		got, err := hostAbsFromSharedWithHome("home/alice", "/home/alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/home/alice" {
			t.Errorf("got %q, want /home/alice", got)
		}
	})

	t.Run("PathOutsideHome", func(t *testing.T) {
		_, err := hostAbsFromSharedWithHome("etc/passwd", "/home/alice")
		if err == nil || !strings.Contains(err.Error(), "path outside home") {
			t.Errorf("expected path outside home error, got %v", err)
		}
	})
}

func TestHostAbsFromSharedWithHomeSymlink(t *testing.T) {
	// Simulate Fedora /home -> /var/home layout.
	dir := t.TempDir()

	varHome := filepath.Join(dir, "var", "home", "alice")
	if err := os.MkdirAll(varHome, 0755); err != nil {
		t.Fatal(err)
	}
	homeLink := filepath.Join(dir, "home")
	if err := os.Symlink(filepath.Join(dir, "var", "home"), homeLink); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(varHome, ".config"), 0755); err != nil {
		t.Fatal(err)
	}

	canonHome := filepath.Join(dir, "var", "home", "alice")
	symlinkHome := filepath.Join(dir, "home", "alice")

	t.Run("CanonicalHomeSymlinkPath", func(t *testing.T) {
		// home is canonical, relPath uses symlink form
		relPath := symlinkHome[1:] + "/.config"
		got, err := hostAbsFromSharedWithHome(relPath, canonHome)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(canonHome, ".config")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("CanonicalHomeSymlinkPathExact", func(t *testing.T) {
		relPath := symlinkHome[1:]
		got, err := hostAbsFromSharedWithHome(relPath, canonHome)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != canonHome {
			t.Errorf("got %q, want %q", got, canonHome)
		}
	})

	t.Run("CanonicalHomeSymlinkPathNonExistent", func(t *testing.T) {
		relPath := symlinkHome[1:] + "/new-dir/sub"
		got, err := hostAbsFromSharedWithHome(relPath, canonHome)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(canonHome, "new-dir", "sub")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("OutsideHomeStillRejected", func(t *testing.T) {
		_, err := hostAbsFromSharedWithHome("etc/passwd", canonHome)
		if err == nil || !strings.Contains(err.Error(), "path outside home") {
			t.Errorf("expected path outside home error, got %v", err)
		}
	})
}
