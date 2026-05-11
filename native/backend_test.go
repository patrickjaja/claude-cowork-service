package native

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalizePath(t *testing.T) {
	t.Run("ExistingPath", func(t *testing.T) {
		dir := t.TempDir()
		got := canonicalizePath(dir)
		want, _ := filepath.EvalSymlinks(dir)
		if got != want {
			t.Errorf("canonicalizePath(%q) = %q, want %q", dir, got, want)
		}
	})

	t.Run("NonExistentLeaf", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "does-not-exist", "also-missing")
		got := canonicalizePath(path)
		want := filepath.Join(dir, "does-not-exist", "also-missing")
		if got != want {
			t.Errorf("canonicalizePath(%q) = %q, want %q", path, got, want)
		}
	})

	t.Run("SymlinkInPrefix", func(t *testing.T) {
		dir := t.TempDir()
		realDir := filepath.Join(dir, "real")
		linkDir := filepath.Join(dir, "link")
		if err := os.Mkdir(realDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realDir, linkDir); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(linkDir, "child", "grandchild")
		got := canonicalizePath(path)
		want := filepath.Join(realDir, "child", "grandchild")
		if got != want {
			t.Errorf("canonicalizePath(%q) = %q, want %q", path, got, want)
		}
	})

	t.Run("Root", func(t *testing.T) {
		got := canonicalizePath("/")
		if got != "/" {
			t.Errorf("canonicalizePath('/') = %q, want '/'", got)
		}
	})
}

func TestResolveSubpath(t *testing.T) {
	t.Run("EmptyRelPath", func(t *testing.T) {
		got := resolveSubpath("/home/alice", "")
		if got != "/home/alice" {
			t.Errorf("got %q, want /home/alice", got)
		}
	})

	t.Run("RootRelativeUnderHome", func(t *testing.T) {
		got := resolveSubpath("/home/alice", "home/alice/.config/Claude")
		if got != "/home/alice/.config/Claude" {
			t.Errorf("got %q, want /home/alice/.config/Claude", got)
		}
	})

	t.Run("RootRelativeExactHome", func(t *testing.T) {
		got := resolveSubpath("/home/alice", "home/alice")
		if got != "/home/alice" {
			t.Errorf("got %q, want /home/alice", got)
		}
	})

	t.Run("LegacyHomeRelative", func(t *testing.T) {
		got := resolveSubpath("/home/alice", ".config/Claude/sessions")
		if got != "/home/alice/.config/Claude/sessions" {
			t.Errorf("got %q, want /home/alice/.config/Claude/sessions", got)
		}
	})

	t.Run("LegacyDotSlash", func(t *testing.T) {
		got := resolveSubpath("/home/alice", "./Documents")
		if got != "/home/alice/Documents" {
			t.Errorf("got %q, want /home/alice/Documents", got)
		}
	})

	t.Run("DeepNestedPath", func(t *testing.T) {
		got := resolveSubpath("/home/alice", "home/alice/a/b/c/d/e")
		if got != "/home/alice/a/b/c/d/e" {
			t.Errorf("got %q, want /home/alice/a/b/c/d/e", got)
		}
	})
}

func TestResolveSubpathSymlink(t *testing.T) {
	// Simulate Fedora-style /home -> /var/home layout in a tmpdir.
	//
	// Real structure:  tmpdir/var/home/alice/
	// Symlink:         tmpdir/home -> tmpdir/var/home
	//
	// os.UserHomeDir() would return the canonical: tmpdir/var/home/alice
	// Client sends paths using the symlink form: home/alice/...
	dir := t.TempDir()

	varHome := filepath.Join(dir, "var", "home", "alice")
	if err := os.MkdirAll(varHome, 0755); err != nil {
		t.Fatal(err)
	}
	homeLink := filepath.Join(dir, "home")
	if err := os.Symlink(filepath.Join(dir, "var", "home"), homeLink); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(varHome, ".config", "Claude")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	canonHome := filepath.Join(dir, "var", "home", "alice")
	symlinkHome := filepath.Join(dir, "home", "alice")

	t.Run("CanonicalHomeSymlinkRelPath", func(t *testing.T) {
		// home = canonical (/var/home/alice), relPath uses symlink form
		relPath := symlinkHome[1:] + "/.config/Claude" // strip leading /
		got := resolveSubpath(canonHome, relPath)
		want := filepath.Join(canonHome, ".config", "Claude")
		if got != want {
			t.Errorf("resolveSubpath(%q, %q) = %q, want %q", canonHome, relPath, got, want)
		}
	})

	t.Run("CanonicalHomeSymlinkRelPathExact", func(t *testing.T) {
		relPath := symlinkHome[1:] // strip leading /
		got := resolveSubpath(canonHome, relPath)
		if got != canonHome {
			t.Errorf("resolveSubpath(%q, %q) = %q, want %q", canonHome, relPath, got, canonHome)
		}
	})

	t.Run("CanonicalHomeSymlinkRelPathNonExistent", func(t *testing.T) {
		// Mount target doesn't exist yet - canonicalizePath should still resolve
		// the existing prefix.
		relPath := symlinkHome[1:] + "/new-project/subdir"
		got := resolveSubpath(canonHome, relPath)
		want := filepath.Join(canonHome, "new-project", "subdir")
		if got != want {
			t.Errorf("resolveSubpath(%q, %q) = %q, want %q", canonHome, relPath, got, want)
		}
	})

	t.Run("SymlinkHomeCanonicalRelPath", func(t *testing.T) {
		// Inverse: home is the symlink form, relPath uses canonical form.
		relPath := canonHome[1:] + "/.config/Claude"
		got := resolveSubpath(symlinkHome, relPath)
		want := filepath.Join(canonHome, ".config", "Claude")
		if got != want {
			t.Errorf("resolveSubpath(%q, %q) = %q, want %q", symlinkHome, relPath, got, want)
		}
	})

	t.Run("NoSymlinkFastPath", func(t *testing.T) {
		// When both sides agree, the fast path should handle it.
		relPath := canonHome[1:] + "/.config/Claude"
		got := resolveSubpath(canonHome, relPath)
		want := filepath.Join(canonHome, ".config", "Claude")
		if got != want {
			t.Errorf("resolveSubpath(%q, %q) = %q, want %q", canonHome, relPath, got, want)
		}
	})

	t.Run("LegacyRelativeWithSymlinkHome", func(t *testing.T) {
		// Legacy home-relative paths should still work regardless.
		got := resolveSubpath(canonHome, ".config/Claude")
		want := filepath.Join(canonHome, ".config", "Claude")
		if got != want {
			t.Errorf("resolveSubpath(%q, %q) = %q, want %q", canonHome, ".config/Claude", got, want)
		}
	})
}
