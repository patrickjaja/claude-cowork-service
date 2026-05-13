package native

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/patrickjaja/claude-cowork-service/pipe"
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

	commandWrapper CommandWrapper

	tracker     *processTracker
	subscribers map[uint64]func(event interface{})
	nextSubID   uint64
	mu          sync.RWMutex
}

// BackendOptions controls optional behavior layered on top of the native
// protocol implementation.
type BackendOptions struct {
	CommandWrapper CommandWrapper
}

// ResolvedMountSpec is a mount after home/root-relative path resolution.
type ResolvedMountSpec struct {
	Path string
	Mode string
}

// SpawnContext contains the host-side session state available to command
// wrappers.
type SpawnContext struct {
	Name           string
	ID             string
	SessionPrefix  string
	RealSessionDir string
	Mounts         map[string]pipe.MountSpec
	ResolvedMounts map[string]ResolvedMountSpec
	RawParams      []byte
}

// CommandWrapper may replace the final command that processTracker launches.
// It runs after native path, env, and argument adaptation has completed.
type CommandWrapper func(ctx SpawnContext, cmd string, args []string, env map[string]string, cwd string) (wrappedCmd string, wrappedArgs []string, wrappedEnv map[string]string, wrappedCwd string, err error)

// NewBackend creates a native backend that runs processes on the host.
func NewBackend(debug bool) *Backend {
	return NewBackendWithOptions(debug, BackendOptions{})
}

// NewBackendWithOptions creates a native backend with optional launch behavior.
func NewBackendWithOptions(debug bool, opts BackendOptions) *Backend {
	b := &Backend{
		debug:          debug,
		commandWrapper: opts.CommandWrapper,
		subscribers:    make(map[uint64]func(event interface{})),
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

func (b *Backend) Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]pipe.MountSpec, rawParams []byte) (string, error) {
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

	resolvedMounts := make(map[string]ResolvedMountSpec, len(mounts))
	for mountName, mount := range mounts {
		hostPath := resolveSubpath(home, mount.Path)
		resolvedMounts[mountName] = ResolvedMountSpec{
			Path: hostPath,
			Mode: mount.Mode,
		}
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

	// Drop empty env vars that might confuse auth (e.g. empty ANTHROPIC_API_KEY).
	for k, v := range env {
		if v == "" {
			delete(env, k)
		}
	}

	// Fall back to the real session dir if the caller sent a cwd that doesn't
	// exist on the host (e.g. a VM-style /sessions/<name> path from a client
	// that hasn't been taught the native layout yet). Without this, exec fails
	// with a misleading "fork/exec <cmd>: no such file or directory" — the
	// missing path is c.Dir, not the binary.
	//
	// Skip this in wrapped mode: sandbox cwds live inside the sandbox and may
	// not exist on the host, but the wrapper is responsible for choosing a
	// valid host-side c.Dir for itself.
	if cwd != "" && b.commandWrapper == nil {
		if _, err := os.Stat(cwd); err != nil {
			if b.debug {
				log.Printf("[native] cwd %s missing, falling back to %s", cwd, realSessionDir)
			}
			cwd = realSessionDir
		}
	}

	if b.commandWrapper != nil {
		cmd = resolveExecutable(cmd, b.debug)
		ctx := SpawnContext{
			Name:           name,
			ID:             id,
			SessionPrefix:  "/sessions/" + name,
			RealSessionDir: realSessionDir,
			Mounts:         mounts,
			ResolvedMounts: resolvedMounts,
			RawParams:      rawParams,
		}
		var err error
		cmd, args, env, cwd, err = b.commandWrapper(ctx, cmd, args, env, cwd)
		if err != nil {
			return "", err
		}
	}

	return b.tracker.spawn(id, cmd, args, env, cwd)
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
