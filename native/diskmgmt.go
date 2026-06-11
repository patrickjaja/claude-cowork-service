package native

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/patrickjaja/claude-cowork-service/pipe"
)

// backupInfix joins a session name and timestamp in pre-stop backup dir
// names: <session>.pre-stop-20060102-150405 (created by StopVM).
const backupInfix = ".pre-stop-"

// backupTimeLayout is the timestamp format StopVM appends to backup names.
const backupTimeLayout = "20060102-150405"

// sessionsRoot returns the directory holding all native session dirs.
func sessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "claude-cowork", "sessions"), nil
}

// isSessionRunning reports whether any process spawned for the session is
// still alive. Dead process ids are pruned from the index as a side effect.
func (b *Backend) isSessionRunning(name string) bool {
	b.mu.RLock()
	ids := make([]string, 0, len(b.sessionProcs[name]))
	for id := range b.sessionProcs[name] {
		ids = append(ids, id)
	}
	b.mu.RUnlock()

	running := false
	var dead []string
	for _, id := range ids {
		if alive, _, err := b.tracker.isRunning(id); err == nil && alive {
			running = true
		} else {
			dead = append(dead, id)
		}
	}
	if len(dead) > 0 {
		b.mu.Lock()
		for _, id := range dead {
			delete(b.sessionProcs[name], id)
		}
		if len(b.sessionProcs[name]) == 0 {
			delete(b.sessionProcs, name)
		}
		b.mu.Unlock()
	}
	return running
}

// dirSize sums the sizes of all regular files under path. Unreadable
// entries are skipped — an approximate answer beats an error here.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// statfsBytes returns (totalBytes, freeBytes) for the filesystem holding
// path, walking up to the nearest existing ancestor if path doesn't exist.
func statfsBytes(path string) (int64, int64) {
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err == nil {
			return int64(st.Blocks) * st.Bsize, int64(st.Bavail) * st.Bsize
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, 0
		}
		path = parent
	}
}

// GetSessionsDiskInfo reports real disk usage: statfs totals for the
// filesystem holding the sessions dir, plus per-session {name, sizeBytes}
// entries (pre-stop backups count toward their parent session). Desktop uses
// this for its low-disk dialogs and workspace-cleanup flow; it filters out
// running sessions itself, and DeleteSessionDirs double-checks.
//
// Note: reporting real freeBytes activates Desktop's disk janitor — periodic
// pruneSessionCaches calls and low-disk dialogs when the host disk runs low.
// That is the intended behavior; before v1.0.58 this returned zeros, which
// short-circuited the janitor entirely.
func (b *Backend) GetSessionsDiskInfo(lowWaterBytes int64) (pipe.SessionsDiskInfo, error) {
	root, err := sessionsRoot()
	if err != nil {
		return pipe.SessionsDiskInfo{}, err
	}

	info := pipe.SessionsDiskInfo{Sessions: []interface{}{}}
	info.TotalBytes, info.FreeBytes = statfsBytes(root)

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return info, nil
		}
		return pipe.SessionsDiskInfo{}, err
	}

	sizes := map[string]int64{}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		owner := name
		if i := strings.Index(name, backupInfix); i > 0 {
			owner = name[:i]
		}
		if _, seen := sizes[owner]; !seen {
			names = append(names, owner)
		}
		sizes[owner] += dirSize(filepath.Join(root, name))
	}
	sort.Strings(names)
	for _, n := range names {
		info.Sessions = append(info.Sessions, map[string]interface{}{
			"name":      n,
			"sizeBytes": sizes[n],
		})
	}

	if b.debug {
		log.Printf("[native] getSessionsDiskInfo lowWaterBytes=%d → total=%d free=%d sessions=%d",
			lowWaterBytes, info.TotalBytes, info.FreeBytes, len(info.Sessions))
	}
	return info, nil
}

// DeleteSessionDirs removes the named session dirs and their pre-stop
// backups. Desktop calls this from its workspace-cleanup dialog after user
// confirmation and only offers non-running sessions; refusing running ones
// here is a second line of defense.
func (b *Backend) DeleteSessionDirs(names []string) (pipe.DeleteSessionDirsResult, error) {
	result := pipe.DeleteSessionDirsResult{Deleted: []string{}, Errors: map[string]string{}}
	root, err := sessionsRoot()
	if err != nil {
		return result, err
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}

	for _, name := range names {
		if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			result.Errors[name] = "invalid session name"
			continue
		}
		if b.isSessionRunning(name) {
			result.Errors[name] = "session has a running process"
			continue
		}
		var firstErr error
		for _, e := range entries {
			en := e.Name()
			if en != name && !strings.HasPrefix(en, name+backupInfix) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(root, en)); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			result.Errors[name] = firstErr.Error()
			continue
		}
		result.Deleted = append(result.Deleted, name)
		log.Printf("[native] deleteSessionDirs: removed %s (incl. backups)", name)
	}
	return result, nil
}

// PruneSessionCaches frees disk space held by daemon-created session
// artifacts. Native sessions have no VM-style caches (the CLI shares the
// user's real home, which must not be touched), but StopVM keeps up to five
// .pre-stop-* backups per session — the native equivalent of "session tmp".
// Backups of running sessions are skipped; backup age comes from the
// timestamp in the dir name (cp -a preserves mtimes, so ModTime lies).
func (b *Backend) PruneSessionCaches(onlyIfFreeBytesBelow int64, includeSessionTmp bool, sessionTmpOlderThanSeconds int64) (pipe.PruneSessionCachesResult, error) {
	result := pipe.PruneSessionCachesResult{
		PrunedSessions:  []string{},
		SkippedSessions: []string{},
		FreedBytes:      0,
		Errors:          map[string]string{},
	}
	if !includeSessionTmp {
		// Backups are session tmp; without the flag nothing is prunable natively.
		return result, nil
	}
	root, err := sessionsRoot()
	if err != nil {
		return result, err
	}
	if onlyIfFreeBytesBelow > 0 {
		if _, free := statfsBytes(root); free >= onlyIfFreeBytesBelow {
			return result, nil
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}

	cutoff := time.Now().Add(-time.Duration(sessionTmpOlderThanSeconds) * time.Second)
	pruned := map[string]bool{}
	skipped := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		i := strings.Index(name, backupInfix)
		if !e.IsDir() || i <= 0 {
			continue
		}
		owner := name[:i]
		if b.isSessionRunning(owner) {
			skipped[owner] = true
			continue
		}
		if sessionTmpOlderThanSeconds > 0 {
			created, err := time.ParseInLocation(backupTimeLayout, name[i+len(backupInfix):], time.Local)
			if err != nil || created.After(cutoff) {
				skipped[owner] = true
				continue
			}
		}
		path := filepath.Join(root, name)
		size := dirSize(path)
		if err := os.RemoveAll(path); err != nil {
			result.Errors[name] = err.Error()
			continue
		}
		result.FreedBytes += size
		pruned[owner] = true
		log.Printf("[native] pruneSessionCaches: removed backup %s (%d bytes)", name, size)
	}

	for owner := range pruned {
		result.PrunedSessions = append(result.PrunedSessions, owner)
	}
	for owner := range skipped {
		if !pruned[owner] {
			result.SkippedSessions = append(result.SkippedSessions, owner)
		}
	}
	sort.Strings(result.PrunedSessions)
	sort.Strings(result.SkippedSessions)
	return result, nil
}
