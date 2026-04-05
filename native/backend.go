package native

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/patrickjaja/claude-cowork-service/process"
)

// resolveSubpath resolves a subpath that may be root-relative or home-relative.
//
// Claude Desktop v1.569.0+ changed getVMStorageSubpath to return root-relative
// subpaths (e.g. "home/user/.config/Claude/...") instead of home-relative ones
// (e.g. ".config/Claude/..."). When joined with os.UserHomeDir() naively, this
// produces doubled paths like "/home/user/home/user/.config/Claude/...".
//
// This function detects the format and returns the correct absolute path:
//   - Root-relative ("home/user/..."): prepend "/" → "/home/user/..."
//   - Home-relative (".config/..."): prepend home → "/home/user/.config/..."
func resolveSubpath(home, relPath string) string {
	if relPath == "" {
		return home
	}
	// Treat relPath as root-absolute: /home/user/.config/…
	asRoot := filepath.Clean("/" + relPath)
	if strings.HasPrefix(asRoot, home+string(filepath.Separator)) || asRoot == home {
		return asRoot // already contains the home directory
	}
	// Legacy home-relative path: .config/…
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
	subscribers []func(event interface{})
	mu          sync.RWMutex
}

// NewBackend creates a native backend that runs processes on the host.
func NewBackend(debug bool) *Backend {
	b := &Backend{
		debug: debug,
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

func (b *Backend) StartVM(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.started = true

	log.Printf("[native] startVM %s — running natively on host", name)

	// Emit startup events asynchronously to avoid race with subscribeEvents
	// (both calls arrive simultaneously on different connections)
	go func() {
		time.Sleep(500 * time.Millisecond)
		b.emitEvent(process.NewStartupStepEvent("CERTIFICATE"))
		b.emitEvent(map[string]string{"type": "vmStarted", "name": name})
		b.emitEvent(process.NewStartupStepEvent("VirtualDiskAttachments"))
		b.emitEvent(process.NewAPIReachableEvent(true))
	}()
	return nil
}

func (b *Backend) StopVM(name string) error {
	b.mu.Lock()
	b.started = false
	b.mu.Unlock()

	b.tracker.killAll()

	if b.debug {
		log.Printf("[native] stopVM %s", name)
	}
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

func (b *Backend) Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]string) (string, error) {
	if b.debug {
		log.Printf("[native] spawn: %s %v (cwd=%s, mounts=%v)", cmd, args, cwd, mounts)
	}

	// Log dispatch-critical env vars and tool args for debugging
	log.Printf("[native] DISPATCH-DEBUG: CLAUDE_CODE_BRIEF=%q", env["CLAUDE_CODE_BRIEF"])
	for i, a := range args {
		if a == "--tools" && i+1 < len(args) {
			log.Printf("[native] DISPATCH-DEBUG: --tools=%s", args[i+1])
		}
		if a == "--allowedTools" && i+1 < len(args) {
			log.Printf("[native] DISPATCH-DEBUG: --allowedTools=%s", args[i+1])
		}
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

	for mountName, relPath := range mounts {
		hostPath := resolveSubpath(home, relPath)
		// Skip mounts whose target is not a directory (e.g. app.asar).
		// Claude Desktop passes every mount as --add-dir to the CLI,
		// which rejects non-directory paths.
		if info, err := os.Stat(hostPath); err == nil && !info.IsDir() {
			if b.debug {
				log.Printf("[native] skip non-directory mount: %s → %s", mountName, hostPath)
			}
			continue
		}
		os.MkdirAll(hostPath, 0755)
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

		os.Remove(linkPath)
		os.Symlink(hostPath, linkPath)
		if b.debug {
			log.Printf("[native] mount: %s → %s", linkPath, hostPath)
		}
	}

	// Create /sessions/<name> symlink so absolute VM paths resolve
	topSessionDir := "/sessions/" + name
	os.MkdirAll("/sessions", 0755) // may fail without root — that's ok
	if _, err := os.Lstat(topSessionDir); err != nil {
		os.Symlink(realSessionDir, topSessionDir)
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
	for mountName, relPath := range mounts {
		if strings.HasPrefix(mountName, ".") || mountName == "uploads" || mountName == "outputs" {
			continue
		}
		wsPath := resolveSubpath(home, relPath)
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
		args = append(args, "--append-system-prompt",
			"IMPORTANT: When sharing files with the user, you MUST pass the absolute file path "+
				"in the `attachments` array parameter of SendUserMessage. Do NOT use computer:// "+
				"links or markdown file links — the user is on a remote client and cannot access "+
				"local paths. After creating a file, call present_files first, then call "+
				"SendUserMessage with both a message and the attachments array containing the file paths.")
		if b.debug {
			log.Printf("[native] injected --append-system-prompt for dispatch file delivery")
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
	for mountName, relPath := range mounts {
		hostPath := resolveSubpath(home, relPath)
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

func (b *Backend) IsProcessRunning(processID string) (bool, error) {
	return b.tracker.isRunning(processID)
}

func (b *Backend) MountPath(name string, hostPath string, guestPath string) error {
	// Paths are already native — no mounting needed
	if b.debug {
		log.Printf("[native] mountPath %s → %s (no-op, paths are native)", hostPath, guestPath)
	}
	return nil
}

func (b *Backend) ReadFile(name string, path string) ([]byte, error) {
	if b.debug {
		log.Printf("[native] readFile %s", path)
	}
	return os.ReadFile(path)
}

func (b *Backend) InstallSdk(name string) error {
	if b.debug {
		log.Printf("[native] installSdk (no-op)")
	}
	return nil
}

func (b *Backend) AddApprovedOauthToken(name string, token string) error {
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
	defer b.mu.Unlock()

	b.subscribers = append(b.subscribers, callback)
	idx := len(b.subscribers) - 1

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if idx < len(b.subscribers) {
			b.subscribers[idx] = nil
		}
	}

	return cancel, nil
}

func (b *Backend) GetDownloadStatus() string {
	return "ready"
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
	subs := make([]func(event interface{}), len(b.subscribers))
	copy(subs, b.subscribers)
	b.mu.RUnlock()

	for _, cb := range subs {
		if cb != nil {
			go cb(event)
		}
	}
}
