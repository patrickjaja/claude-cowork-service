package native

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setupSessions points HOME at a temp dir and creates:
//
//	alpha/                              1000 bytes
//	alpha.pre-stop-20200101-000000/      500 bytes (old backup)
//	beta/                                200 bytes
func setupSessions(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".local", "share", "claude-cowork", "sessions")
	writeSizedFile(t, filepath.Join(root, "alpha", "work.txt"), 1000)
	writeSizedFile(t, filepath.Join(root, "alpha"+backupInfix+"20200101-000000", "work.txt"), 500)
	writeSizedFile(t, filepath.Join(root, "beta", "notes.txt"), 200)
	return root
}

func TestGetSessionsDiskInfoReportsSessions(t *testing.T) {
	setupSessions(t)
	b := NewBackend(false)

	info, err := b.GetSessionsDiskInfo(0)
	if err != nil {
		t.Fatalf("GetSessionsDiskInfo: %v", err)
	}
	if info.TotalBytes <= 0 || info.FreeBytes <= 0 {
		t.Fatalf("expected real statfs numbers, got total=%d free=%d", info.TotalBytes, info.FreeBytes)
	}

	sizes := map[string]int64{}
	for _, s := range info.Sessions {
		entry, ok := s.(map[string]interface{})
		if !ok {
			t.Fatalf("session entry is %T, want map", s)
		}
		sizes[entry["name"].(string)] = entry["sizeBytes"].(int64)
	}
	if len(sizes) != 2 {
		t.Fatalf("sessions = %v, want alpha and beta", sizes)
	}
	if sizes["alpha"] != 1500 {
		t.Fatalf("alpha sizeBytes = %d, want 1500 (dir + backup)", sizes["alpha"])
	}
	if sizes["beta"] != 200 {
		t.Fatalf("beta sizeBytes = %d, want 200", sizes["beta"])
	}
}

func TestDeleteSessionDirsRemovesDirAndBackups(t *testing.T) {
	root := setupSessions(t)
	b := NewBackend(false)

	result, err := b.DeleteSessionDirs([]string{"alpha", "../evil"})
	if err != nil {
		t.Fatalf("DeleteSessionDirs: %v", err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != "alpha" {
		t.Fatalf("deleted = %v, want [alpha]", result.Deleted)
	}
	if _, ok := result.Errors["../evil"]; !ok {
		t.Fatalf("expected error for path-traversal name, got %v", result.Errors)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Fatal("alpha dir still exists")
	}
	if _, err := os.Stat(filepath.Join(root, "alpha"+backupInfix+"20200101-000000")); !os.IsNotExist(err) {
		t.Fatal("alpha backup still exists")
	}
	if _, err := os.Stat(filepath.Join(root, "beta")); err != nil {
		t.Fatalf("beta should be untouched: %v", err)
	}
}

func TestPruneSessionCachesRemovesOldBackups(t *testing.T) {
	root := setupSessions(t)
	b := NewBackend(false)

	result, err := b.PruneSessionCaches(0, true, 0)
	if err != nil {
		t.Fatalf("PruneSessionCaches: %v", err)
	}
	if result.FreedBytes != 500 {
		t.Fatalf("freedBytes = %d, want 500", result.FreedBytes)
	}
	if len(result.PrunedSessions) != 1 || result.PrunedSessions[0] != "alpha" {
		t.Fatalf("prunedSessions = %v, want [alpha]", result.PrunedSessions)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha"+backupInfix+"20200101-000000")); !os.IsNotExist(err) {
		t.Fatal("backup still exists after prune")
	}
	// The session dirs themselves hold user work products — never pruned.
	if _, err := os.Stat(filepath.Join(root, "alpha")); err != nil {
		t.Fatalf("alpha session dir must survive pruning: %v", err)
	}
}

func TestPruneSessionCachesHonorsFlagsAndAge(t *testing.T) {
	root := setupSessions(t)
	// Add a fresh backup (timestamp = now) next to the old one.
	freshBackup := "beta" + backupInfix + time.Now().Format(backupTimeLayout)
	writeSizedFile(t, filepath.Join(root, freshBackup, "x"), 100)
	b := NewBackend(false)

	// includeSessionTmp=false → nothing prunable natively.
	result, err := b.PruneSessionCaches(0, false, 0)
	if err != nil {
		t.Fatalf("PruneSessionCaches: %v", err)
	}
	if result.FreedBytes != 0 || len(result.PrunedSessions) != 0 {
		t.Fatalf("expected no-op without includeSessionTmp, got %+v", result)
	}

	// onlyIfFreeBytesBelow=1 → free space is certainly above 1 byte, skip.
	result, err = b.PruneSessionCaches(1, true, 0)
	if err != nil {
		t.Fatalf("PruneSessionCaches: %v", err)
	}
	if result.FreedBytes != 0 {
		t.Fatalf("expected skip above free-space threshold, got %+v", result)
	}

	// Age filter: only the 2020 backup is older than one hour.
	result, err = b.PruneSessionCaches(0, true, 3600)
	if err != nil {
		t.Fatalf("PruneSessionCaches: %v", err)
	}
	if result.FreedBytes != 500 {
		t.Fatalf("freedBytes = %d, want 500 (old backup only)", result.FreedBytes)
	}
	if len(result.SkippedSessions) != 1 || result.SkippedSessions[0] != "beta" {
		t.Fatalf("skippedSessions = %v, want [beta] (fresh backup)", result.SkippedSessions)
	}
	if _, err := os.Stat(filepath.Join(root, freshBackup)); err != nil {
		t.Fatalf("fresh backup must survive age-filtered prune: %v", err)
	}
}
