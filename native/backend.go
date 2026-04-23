package native

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patrickjaja/claude-cowork-service/pipe"
	"github.com/patrickjaja/claude-cowork-service/process"
)

// canonicalizePath resolves symlinks in the longest existing prefix of path.
// Handles paths where leaf components don't yet exist on disk by walking up
// to the nearest existing ancestor and resolving from there.
func canonicalizePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	dir := filepath.Dir(path)
	if dir == path {
		return path
	}
	return filepath.Join(canonicalizePath(dir), filepath.Base(path))
}

// resolveSubpath resolves a subpath that may be root-relative or home-relative.
//
// Claude Desktop v1.569.0+ changed getVMStorageSubpath to return root-relative
// subpaths (e.g. "home/user/.config/Claude/...") instead of home-relative ones
// (e.g. ".config/Claude/..."). When joined with os.UserHomeDir() naively, this
// produces doubled paths like "/home/user/home/user/.config/Claude/...".
//
// This function detects the format and returns the correct absolute path:
//   - Root-relative ("home/user/..."): prepend "/" -> "/home/user/..."
//   - Home-relative (".config/..."): prepend home -> "/home/user/.config/..."
//
// On systems where /home is a symlink (e.g. /home -> /var/home on Fedora
// Silverblue), the string prefix check may fail because os.UserHomeDir()
// returns the canonical form while the client sends the symlink form. The
// slow path resolves symlinks on both sides before comparing.
func resolveSubpath(home, relPath string) string {
	if relPath == "" {
		return home
	}
	// Treat relPath as root-absolute: /home/user/.config/...
	asRoot := filepath.Clean("/" + relPath)
	sep := string(filepath.Separator)
	if strings.HasPrefix(asRoot, home+sep) || asRoot == home {
		return asRoot
	}
	// Slow path: resolve symlinks to handle /home -> /var/home style layouts.
	homeCanon := canonicalizePath(home)
	asRootCanon := canonicalizePath(asRoot)
	if strings.HasPrefix(asRootCanon, homeCanon+sep) || asRootCanon == homeCanon {
		return asRootCanon
	}
	// Legacy home-relative path: .config/...
	return filepath.Join(home, relPath)
}

// Backend implements pipe.VMBackend by executing commands directly on the host.
// No VM is involved — lifecycle methods satisfy the protocol with instant success.
type Backend struct {
	debug   bool
	started bool
	memory  int
	cpus    int

	tracker     *processTracker
	subscribers map[uint64]func(event interface{})
	nextSubID   uint64
	mu          sync.RWMutex
}

// NewBackend creates a native backend that runs processes on the host.
func NewBackend(debug bool) *Backend {
	b := &Backend{
		debug:       debug,
		subscribers: make(map[uint64]func(event interface{})),
	}
	b.tracker = newProcessTracker(b.emitEvent, debug)
	return b
}

func (b *Backend) Configure(memoryMB int, cpuCount int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if memoryMB > 0 {
		b.memory = memoryMB
	}
	if cpuCount > 0 {
		b.cpus = cpuCount
	}

	if b.debug {
		log.Printf("[native] configured: memoryMB=%d, cpuCount=%d (ignored, running natively)", b.memory, b.cpus)
	}
	return nil
}

func (b *Backend) CreateVM(name string) error {
	if b.debug {
		log.Printf("[native] createVM %s (no-op)", name)
	}
	return nil
}

func (b *Backend) StartVM(name string, bundlePath string, memoryGB int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.started = true

	log.Printf("[native] startVM %s — running natively on host", name)

	// Run session integrity checks in background (non-blocking).
	// Scans for half-written JSONL files, orphaned pre-stop backups,
	// and other signs of previous unclean shutdown.
	go b.checkSessionIntegrity(name)

	// Emit startup events asynchronously to avoid race with subscribeEvents
	// (both calls arrive simultaneously on different connections)
	go func() {
		time.Sleep(500 * time.Millisecond)
		b.emitEvent(process.NewStartupStepEvent("CERTIFICATE", "started"))
		b.emitEvent(process.NewStartupStepEvent("CERTIFICATE", "completed"))
		b.emitEvent(map[string]string{"type": "vmStarted", "name": name})
		b.emitEvent(process.NewStartupStepEvent("VirtualDiskAttachments", "started"))
		b.emitEvent(process.NewStartupStepEvent("VirtualDiskAttachments", "completed"))
		b.emitEvent(process.NewNetworkStatusEvent(true))
		b.emitEvent(process.NewAPIReachableEvent(true))
	}()
	return nil
}

func (b *Backend) StopVM(name string) error {
	if b.debug {
		log.Printf("[native] stopVM %s — initiating graceful shutdown", name)
	}

	// Backup session files before killing processes.
	// This preserves the queue JSONL state in case the graceful drain
	// in kill() doesn't complete before the process is terminated.
	home, _ := os.UserHomeDir()
	sessionDir := filepath.Join(home, ".local", "share", "claude-cowork", "sessions", name)
	if _, err := os.Stat(sessionDir); err == nil {
		backupDir := sessionDir + ".pre-stop-" + time.Now().Format("20060102-150405")
		if cpErr := exec.Command("cp", "-a", sessionDir, backupDir).Run(); cpErr != nil {
			log.Printf("[native] WARNING: pre-stop backup failed: %v", cpErr)
		} else {
			log.Printf("[native] pre-stop backup created: %s", backupDir)
			// Prune old backups: keep only the 5 most recent
			go pruneBackups(sessionDir, 5)
		}
	}

	b.mu.Lock()
	b.started = false
	b.mu.Unlock()

	b.tracker.killAll()

	b.emitEvent(map[string]string{"type": "vmStopped", "name": name})
	return nil
}

func (b *Backend) IsRunning(name string) (bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.started, nil
}

func (b *Backend) IsGuestConnected(name string) (bool, error) {
	// On native Linux, the host IS the guest — always connected.
	// Claude Desktop calls this before startVM to check if it can skip boot.
	// Returning false causes repeated polling until timeout.
	return true, nil
}

func (b *Backend) Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]pipe.MountSpec, _ []byte) (string, error) {
	if b.debug {
		log.Printf("[native] spawn: %s %v (cwd=%s, mounts=%v)", cmd, args, cwd, mounts)
	}

	// The client sends VM paths like /sessions/<name>/mnt/<mount>.
	// We create these under ~/.local/share/claude-cowork/sessions/ and
	// symlink /sessions/<name> → there so the absolute paths work.
	home, _ := os.UserHomeDir()
	realSessionDir := filepath.Join(home, ".local", "share", "claude-cowork", "sessions", name)
	mntDir := filepath.Join(realSessionDir, "mnt")
	if err := os.MkdirAll(mntDir, 0755); err != nil {
		return "", fmt.Errorf("creating session dir: %w", err)
	}

	for mountName, mount := range mounts {
		hostPath := resolveSubpath(home, mount.Path)
		// Skip mounts whose target is not a directory (e.g. app.asar).
		// Claude Desktop passes every mount as --add-dir to the CLI,
		// which rejects non-directory paths.
		if info, err := os.Stat(hostPath); err == nil && !info.IsDir() {
			if b.debug {
				log.Printf("[native] skip non-directory mount: %s → %s", mountName, hostPath)
			}
			continue
		}
		if err := os.MkdirAll(hostPath, 0755); err != nil && b.debug {
			log.Printf("[native] MkdirAll %s: %v", hostPath, err)
		}
		linkPath := filepath.Join(mntDir, mountName)

		// Prevent self-referencing symlinks (ELOOP bug).
		// When a parent mount (e.g. ".remote-plugins") is already symlinked,
		// child mounts (e.g. ".remote-plugins/<id>/.mcpb-cache") resolve
		// through the parent symlink into the real filesystem, making
		// linkPath and hostPath point to the same location. Creating a
		// symlink there would make it point to itself → ELOOP on every access.
		if resolved, err := filepath.EvalSymlinks(filepath.Dir(linkPath)); err == nil {
			resolvedLink := filepath.Join(resolved, filepath.Base(linkPath))
			if resolvedLink == hostPath {
				if b.debug {
					log.Printf("[native] skip self-referencing mount: %s (resolves to %s)", mountName, hostPath)
				}
				continue
			}
		}

		if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) && b.debug {
			log.Printf("[native] remove stale link %s: %v", linkPath, err)
		}
		if err := os.Symlink(hostPath, linkPath); err != nil {
			if b.debug {
				log.Printf("[native] symlink %s → %s: %v", linkPath, hostPath, err)
			}
			continue
		}
		if b.debug {
			log.Printf("[native] mount: %s → %s", linkPath, hostPath)
		}
	}

	// Create /sessions/<name> symlink so absolute VM paths resolve.
	// MkdirAll and Symlink both fail without root, which is the common case —
	// the caller already copes by path-remapping cwd/env in that mode.
	topSessionDir := "/sessions/" + name
	if err := os.MkdirAll("/sessions", 0755); err != nil && b.debug {
		log.Printf("[native] MkdirAll /sessions: %v (expected without root)", err)
	}
	if _, err := os.Lstat(topSessionDir); err != nil {
		if err := os.Symlink(realSessionDir, topSessionDir); err != nil && b.debug {
			log.Printf("[native] symlink %s → %s: %v (expected without root)", topSessionDir, realSessionDir, err)
		}
	}

	// Session prefix used for path remapping (VM paths ↔ real paths)
	sessionPrefix := "/sessions/" + name

	// If /sessions isn't writable (no root), remap cwd and env to real paths
	if _, err := os.Stat(cwd); err != nil {
		// Replace the /sessions/<name> prefix with the real session dir,
		// preserving any sub-path (e.g. /mnt/outputs). Using filepath.Base
		// here would drop everything but the final segment, turning
		// /sessions/<name>/mnt/outputs into realSessionDir/outputs and
		// triggering a child-side chdir() failure that surfaces as a
		// misleading "fork/exec: no such file or directory" error.
		var remapped string
		if cwd == sessionPrefix || strings.HasPrefix(cwd, sessionPrefix+"/") {
			remapped = realSessionDir + cwd[len(sessionPrefix):]
		} else {
			// Fallback: cwd doesn't reference the session prefix at all.
			remapped = filepath.Join(realSessionDir, filepath.Base(cwd))
		}
		if b.debug {
			log.Printf("[native] remap cwd: %s → %s", cwd, remapped)
		}
		cwd = remapped

		// Remap env vars and args pointing to /sessions/<name>
		for k, v := range env {
			if len(v) >= len(sessionPrefix) && v[:len(sessionPrefix)] == sessionPrefix {
				env[k] = realSessionDir + v[len(sessionPrefix):]
				if b.debug {
					log.Printf("[native] remap env %s: %s", k, env[k])
				}
			}
		}
		for i, a := range args {
			if len(a) >= len(sessionPrefix) && a[:len(sessionPrefix)] == sessionPrefix {
				args[i] = realSessionDir + a[len(sessionPrefix):]
				if b.debug {
					log.Printf("[native] remap arg[%d]: %s", i, args[i])
				}
			}
		}
	}

	// Use the real workspace directory as cwd instead of the session directory.
	// The session's mnt/ dir uses symlinks for mounts, but Glob doesn't follow
	// directory symlinks, so files aren't found. Setting cwd to the actual
	// workspace path lets the model search real files directly.
	for mountName, mount := range mounts {
		if strings.HasPrefix(mountName, ".") || mountName == "uploads" || mountName == "outputs" {
			continue
		}
		wsPath := resolveSubpath(home, mount.Path)
		if info, err := os.Stat(wsPath); err == nil && info.IsDir() {
			if b.debug {
				log.Printf("[native] using workspace mount %q as cwd: %s (was %s)", mountName, wsPath, cwd)
			}
			cwd = wsPath
		}
		break
	}

	// Remove empty env vars that might confuse auth (e.g. empty ANTHROPIC_API_KEY)
	for k, v := range env {
		if v == "" {
			delete(env, k)
		}
	}

	// SDK MCP servers (dispatch, cowork, session_info, etc.) are kept in
	// --mcp-config as {type:"sdk"} stubs. The CLI sends control_request
	// messages on stdout for MCP tool calls, which flow through our event
	// stream to Claude Desktop. Desktop's session manager handles them and
	// sends control_response back via writeStdin — identical to VM mode.
	// No stripping or proxying needed on our side.
	if b.debug {
		for i, a := range args {
			if a == "--mcp-config" && i+1 < len(args) {
				log.Printf("[native] --mcp-config passed through (SDK MCP proxy via event stream): %s", args[i+1])
				break
			}
		}
	}

	// Strip --disallowedTools entirely on native Linux.
	//
	// Desktop passes --disallowedTools for VM-based sessions where certain tools
	// (present_files, allow_cowork_file_delete, launch_code_session, create_artifact,
	// update_artifact) are handled by the VM runtime rather than the CLI. On native
	// Linux there is no VM — the CLI must handle all tools directly.
	//
	// Default --disallowedTools from Desktop (as of v1.569.0, unchanged from v1.1.9669):
	//   AskUserQuestion, mcp__cowork__allow_cowork_file_delete,
	//   mcp__cowork__present_files, mcp__cowork__launch_code_session,
	//   mcp__cowork__create_artifact, mcp__cowork__update_artifact
	//
	// We remove the entire flag so all tools are available to the CLI.
	for i, a := range args {
		if a == "--disallowedTools" && i+1 < len(args) {
			if b.debug {
				log.Printf("[native] stripping --disallowedTools (VM-only restriction): %s", args[i+1])
			}
			// Remove both the flag and its value by blanking them
			args = append(args[:i], args[i+2:]...)
			break
		}
	}

	// Inject --brief flag when Desktop signals dispatch/agent mode via CLAUDE_CODE_BRIEF=1.
	// Desktop passes this env var for ditto/dispatch agent sessions (which have SendUserMessage
	// in --tools), but NOT for regular cowork sessions. The --brief flag ensures the CLI
	// registers SendUserMessage in its tool list (fixed in CLI v2.1.86).
	if env["CLAUDE_CODE_BRIEF"] == "1" {
		hasBrief := false
		for _, a := range args {
			if a == "--brief" {
				hasBrief = true
				break
			}
		}
		if !hasBrief {
			args = append(args, "--brief")
			if b.debug {
				log.Printf("[native] injected --brief flag (CLAUDE_CODE_BRIEF=1)")
			}
		}

		// Inject file-delivery instructions for dispatch/agent sessions.
		// The user is on a remote client (phone/browser) and can't access local paths.
		// Without this, the model often uses computer:// markdown links instead of the
		// attachments parameter, and files never reach the remote user.
		//
		// Also tell the model the real outputs path so it doesn't waste tool calls
		// trying /sessions/ paths (which only exist when /sessions is root-writable).
		outputsHint := ""
		for mountName, mount := range mounts {
			if mountName == "outputs" {
				hostOutputs := resolveSubpath(home, mount.Path)
				outputsHint = " The outputs directory for this session is at: " + hostOutputs +
					" — write files there directly. The /sessions/ directory does NOT exist in this environment."
				break
			}
		}
		args = append(args, "--append-system-prompt",
			"IMPORTANT: When sharing files with the user, you MUST pass the absolute file path "+
				"in the `attachments` array parameter of SendUserMessage. Do NOT use computer:// "+
				"links or markdown file links — the user is on a remote client and cannot access "+
				"local paths. After creating a file, call present_files first, then call "+
				"SendUserMessage with both a message and the attachments array containing the file paths."+
				outputsHint)
		if b.debug {
			log.Printf("[native] injected --append-system-prompt for dispatch file delivery (outputsHint=%q)", outputsHint)
		}
	}

	// Build mount path remappings (forward and reverse).
	//
	// Forward (stdin, Desktop→CLI): session/mnt/<mount> → real target path
	//   Glob doesn't follow directory symlinks, so the model must see real paths.
	//
	// Reverse (stdout, CLI→Desktop): real target path → VM /sessions/<name>/mnt/<mount>
	//   Desktop's MCP tools expect VM-style paths. Without reverse mapping, tools
	//   like present_files fail because Desktop can't resolve native Linux paths.
	var mountRemap []pathRemap
	var reverseMountRemap []pathRemap
	for mountName, mount := range mounts {
		hostPath := resolveSubpath(home, mount.Path)
		mntPath := realSessionDir + "/mnt/" + mountName
		vmMntPath := sessionPrefix + "/mnt/" + mountName
		if mntPath != hostPath {
			mountRemap = append(mountRemap, pathRemap{
				from: []byte(mntPath),
				to:   []byte(hostPath),
			})
			if b.debug {
				log.Printf("[native] mount remap (fwd): %s → %s", mntPath, hostPath)
			}
		}
		// Reverse: real host path → VM mount path (for outgoing MCP requests)
		if hostPath != vmMntPath {
			reverseMountRemap = append(reverseMountRemap, pathRemap{
				from: []byte(hostPath),
				to:   []byte(vmMntPath),
			})
			if b.debug {
				log.Printf("[native] mount remap (rev): %s → %s", hostPath, vmMntPath)
			}
		}
	}

	return b.tracker.spawn(id, cmd, args, env, cwd, sessionPrefix, realSessionDir, mountRemap, reverseMountRemap)
}

func (b *Backend) Kill(processID string, signal string) error {
	if b.debug {
		log.Printf("[native] kill %s (signal=%s)", processID, signal)
	}
	return b.tracker.kill(processID, signal)
}

func (b *Backend) WriteStdin(processID string, data []byte) error {
	return b.tracker.writeStdin(processID, data)
}

func (b *Backend) IsProcessRunning(processID string) (bool, int, error) {
	return b.tracker.isRunning(processID)
}

func (b *Backend) MountPath(processID string, subpath string, mountName string, mode string) error {
	// Paths are already native — no mounting needed. Spawn handles the
	// per-session symlink layout via additionalMounts instead.
	if b.debug {
		log.Printf("[native] mountPath processId=%s subpath=%s mountName=%s mode=%s (no-op, paths are native)", processID, subpath, mountName, mode)
	}
	return nil
}

func (b *Backend) ReadFile(processName string, filePath string) ([]byte, error) {
	if b.debug {
		log.Printf("[native] readFile %s", filePath)
	}
	return os.ReadFile(filePath)
}

func (b *Backend) InstallSdk(sdkSubpath string, version string) error {
	if b.debug {
		log.Printf("[native] installSdk %s@%s (no-op)", sdkSubpath, version)
	}
	return nil
}

func (b *Backend) AddApprovedOauthToken(token string) error {
	if b.debug {
		log.Printf("[native] addApprovedOauthToken (no-op)")
	}
	return nil
}

func (b *Backend) SetDebugLogging(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.debug = enabled
	b.tracker.debug = enabled
	if enabled {
		log.Printf("[native] debug logging enabled")
	}
}

func (b *Backend) SubscribeEvents(name string, callback func(event interface{})) (func(), error) {
	b.mu.Lock()
	b.nextSubID++
	id := b.nextSubID
	b.subscribers[id] = callback
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		delete(b.subscribers, id)
		b.mu.Unlock()
	}

	return cancel, nil
}

// Touch is part of pipe.VMBackend; native has no dead-client watchdog, so no-op.
func (b *Backend) Touch() {}

func (b *Backend) GetDownloadStatus() string {
	return "ready"
}

func (b *Backend) GetSessionsDiskInfo(lowWaterBytes int64) (pipe.SessionsDiskInfo, error) {
	if b.debug {
		log.Printf("[native] getSessionsDiskInfo lowWaterBytes=%d (no-op, native mode)", lowWaterBytes)
	}
	return pipe.SessionsDiskInfo{
		TotalBytes: 0,
		FreeBytes:  0,
		Sessions:   []interface{}{},
	}, nil
}

func (b *Backend) DeleteSessionDirs(names []string) (pipe.DeleteSessionDirsResult, error) {
	if b.debug {
		log.Printf("[native] deleteSessionDirs names=%v (no-op, native mode)", names)
	}
	return pipe.DeleteSessionDirsResult{
		Deleted: []string{},
		Errors:  map[string]string{},
	}, nil
}

func (b *Backend) CreateDiskImage(diskName string, sizeGiB int) error {
	if b.debug {
		log.Printf("[native] createDiskImage diskName=%q sizeGiB=%d (no-op, native mode)", diskName, sizeGiB)
	}
	return nil
}

func (b *Backend) SendGuestResponse(id string, resultJSON string, errMsg string) error {
	// No-op on native Linux — guest responses are delivered via the filesystem
	// permission bridge (plugin shims write to .cowork-perm-req, host writes
	// responses to .cowork-perm-resp). In VM mode this RPC delivers responses
	// over vsock, but on native the shim and host share the same filesystem.
	if b.debug {
		log.Printf("[native] sendGuestResponse id=%s (no-op, native mode)", id)
	}
	return nil
}

// Shutdown kills all tracked processes.
func (b *Backend) Shutdown() {
	log.Printf("[native] shutting down...")
	b.tracker.killAll()
}

func (b *Backend) emitEvent(event interface{}) {
	b.mu.RLock()
	subs := make([]func(event interface{}), 0, len(b.subscribers))
	for _, cb := range b.subscribers {
		subs = append(subs, cb)
	}
	b.mu.RUnlock()

	for _, cb := range subs {
		go cb(event)
	}
}

// checkSessionIntegrity runs background integrity checks on session files
// for the given VM name. This detects signs of previous unclean shutdowns
// (truncated JSONL, orphaned backups) and logs warnings.
//
// This intentionally does NOT auto-repair — that's left to cowork-session-doctor
// which can be run interactively. We only log diagnostics here.
func (b *Backend) checkSessionIntegrity(name string) {
	home, _ := os.UserHomeDir()
	sessionsDir := filepath.Join(home, ".local", "share", "claude-cowork", "sessions")

	if _, err := os.Stat(sessionsDir); err != nil {
		return
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return
	}

	backupCount := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		eName := e.Name()

		// Count orphaned pre-stop backups
		if strings.Contains(eName, ".pre-stop-") {
			backupCount++
			continue
		}

		// Check session directories for JSONL integrity
		sessionDir := filepath.Join(sessionsDir, eName)
		auditPath := filepath.Join(sessionDir, "audit.jsonl")
		if info, err := os.Stat(auditPath); err == nil {
			if info.Size() == 0 {
				log.Printf("[native] INTEGRITY WARNING: empty audit.jsonl in session %s", eName)
			} else {
				// Check if audit.jsonl ends with a complete line (no truncation)
				f, err := os.Open(auditPath)
				if err == nil {
					buf := make([]byte, 1)
					f.Seek(info.Size()-1, 0)
					n, _ := f.Read(buf)
					if n > 0 && buf[0] != '\n' {
						log.Printf("[native] INTEGRITY WARNING: audit.jsonl in session %s may be truncated (no trailing newline)", eName)
					}
					f.Close()
				}
			}
		}
	}

	if backupCount > 0 {
		log.Printf("[native] startup: found %d pre-stop backup(s) in %s", backupCount, sessionsDir)
	}
}

// pruneBackups removes old pre-stop backup directories, keeping only the
// `keep` most recent ones. Backups are identified by the ".pre-stop-" suffix
// pattern in the session directory's parent.
func pruneBackups(sessionDir string, keep int) {
	parent := filepath.Dir(sessionDir)
	base := filepath.Base(sessionDir)
	prefix := base + ".pre-stop-"

	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}

	var backups []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			backups = append(backups, filepath.Join(parent, e.Name()))
		}
	}

	// Sort lexicographically (timestamp suffix ensures chronological order)
	sort.Strings(backups)

	// Remove oldest backups, keeping `keep` most recent
	if len(backups) > keep {
		for _, old := range backups[:len(backups)-keep] {
			if err := os.RemoveAll(old); err != nil {
				log.Printf("[native] failed to prune backup %s: %v", old, err)
			} else {
				log.Printf("[native] pruned old backup: %s", old)
			}
		}
	}
}
