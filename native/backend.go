package native

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/patrickjaja/claude-cowork-service/process"
)

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

func (b *Backend) Configure(memory int, cpus int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if memory > 0 {
		b.memory = memory
	}
	if cpus > 0 {
		b.cpus = cpus
	}

	if b.debug {
		log.Printf("[native] configured: memory=%dMB, cpus=%d (ignored, running natively)", b.memory, b.cpus)
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
		b.emitEvent(map[string]string{"type": "vmStarted", "name": name})
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
	b.mu.RLock()
	defer b.mu.RUnlock()
	// On native, the "guest" is always connected once started
	return b.started, nil
}

func (b *Backend) Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]string) (string, error) {
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

	for mountName, relPath := range mounts {
		hostPath := filepath.Join(home, relPath)
		os.MkdirAll(hostPath, 0755)
		linkPath := filepath.Join(mntDir, mountName)
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
		remapped := filepath.Join(realSessionDir, filepath.Base(cwd))
		// cwd is just the session dir itself (/sessions/<name>)
		if filepath.Base(cwd) == name {
			remapped = realSessionDir
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

	// Remove empty env vars that might confuse auth (e.g. empty ANTHROPIC_API_KEY)
	for k, v := range env {
		if v == "" {
			delete(env, k)
		}
	}

	// Strip --mcp-config with sdk-type servers — we can't provide them
	// as they require the parent to proxy stdio connections.
	// Replace the config with an empty one so Claude Code starts without blocking.
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) {
			args[i+1] = `{"mcpServers":{}}`
			if b.debug {
				log.Printf("[native] stripped sdk MCP servers from --mcp-config")
			}
			break
		}
	}

	return b.tracker.spawn(id, cmd, args, env, cwd, sessionPrefix, realSessionDir)
}

func (b *Backend) Kill(processID string) error {
	if b.debug {
		log.Printf("[native] kill %s", processID)
	}
	return b.tracker.kill(processID)
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
	return "Ready"
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
