package vm

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Manager coordinates VM lifecycle, bundles, and guest communication.
// It implements the pipe.VMBackend interface.
type Manager struct {
	dataDir    string
	bundlesDir string // Claude Desktop's bundle storage path
	debug      bool
	memory     int // MB, default 4096
	cpus       int // default 2
	cid        uint32

	bundles  *BundleManager
	instance *QEMUInstance
	vsock    *VsockListener

	subscribers []func(event interface{})
	mu          sync.RWMutex
}

// NewManager creates a new VM manager.
// bundlesDir is where Claude Desktop stores downloaded VM bundles
// (typically ~/.config/Claude/vm_bundles).
func NewManager(dataDir string, bundlesDir string, debug bool) *Manager {
	return &Manager{
		dataDir:    dataDir,
		bundlesDir: bundlesDir,
		debug:      debug,
		memory:     4096,
		cpus:       2,
		cid:        3, // Default guest CID
		bundles:    NewBundleManager(dataDir, debug),
	}
}

func (m *Manager) Configure(memory int, cpus int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if memory > 0 {
		m.memory = memory
	}
	if cpus > 0 {
		m.cpus = cpus
	}

	if m.debug {
		log.Printf("Configured: memory=%dMB, cpus=%d", m.memory, m.cpus)
	}
	return nil
}

func (m *Manager) CreateVM(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create VM state directory
	stateDir := filepath.Join(m.dataDir, "state", name)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("creating VM state dir: %w", err)
	}

	log.Printf("VM %s created (state: %s)", name, stateDir)
	return nil
}

func (m *Manager) StartVM(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.instance != nil && m.instance.IsRunning() {
		return fmt.Errorf("a VM is already running")
	}

	// Find the latest bundle
	bundleDir, err := m.findLatestBundle()
	if err != nil {
		return fmt.Errorf("no VM bundle available: %w", err)
	}

	// Prepare bundle (decompress, convert)
	if err := m.bundles.PrepareBundle(bundleDir); err != nil {
		return fmt.Errorf("preparing bundle: %w", err)
	}

	// Create and start QEMU instance
	m.instance = NewQEMUInstance(name, m.dataDir, bundleDir, m.memory, m.cpus, m.cid)
	if err := m.instance.Start(); err != nil {
		m.instance = nil
		return fmt.Errorf("starting VM: %w", err)
	}

	// Start vsock listener for sdk-daemon communication
	m.vsock = NewVsockListener(vsockPort, m.debug)
	if err := m.vsock.Listen(); err != nil {
		log.Printf("Warning: vsock listener failed: %v (sdk-daemon communication unavailable)", err)
		// Don't fail - VM can still run, just no guest communication
	}

	m.emitEvent(map[string]string{"type": "vmStarted", "name": name})
	return nil
}

func (m *Manager) StopVM(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vsock != nil {
		m.vsock.Close()
		m.vsock = nil
	}

	if m.instance == nil {
		return nil
	}

	if err := m.instance.Stop(); err != nil {
		return err
	}
	m.instance = nil

	m.emitEvent(map[string]string{"type": "vmStopped", "name": name})
	return nil
}

func (m *Manager) IsRunning(name string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.instance == nil {
		return false, nil
	}
	return m.instance.IsRunning(), nil
}

func (m *Manager) IsGuestConnected(name string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil {
		return false, nil
	}
	return m.vsock.IsConnected(), nil
}

func (m *Manager) Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil || !m.vsock.IsConnected() {
		return "", fmt.Errorf("sdk-daemon not connected")
	}

	resp, err := m.vsock.SendCommand(map[string]interface{}{
		"method": "spawn",
		"cmd":    cmd,
		"args":   args,
		"env":    env,
		"cwd":    cwd,
	})
	if err != nil {
		return "", err
	}

	var result struct {
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parsing spawn response: %w", err)
	}

	return result.ProcessID, nil
}

func (m *Manager) Kill(processID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil || !m.vsock.IsConnected() {
		return fmt.Errorf("sdk-daemon not connected")
	}

	_, err := m.vsock.SendCommand(map[string]interface{}{
		"method":    "kill",
		"processId": processID,
	})
	return err
}

func (m *Manager) WriteStdin(processID string, data []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil || !m.vsock.IsConnected() {
		return fmt.Errorf("sdk-daemon not connected")
	}

	_, err := m.vsock.SendCommand(map[string]interface{}{
		"method":    "writeStdin",
		"processId": processID,
		"data":      string(data),
	})
	return err
}

func (m *Manager) IsProcessRunning(processID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil || !m.vsock.IsConnected() {
		return false, fmt.Errorf("sdk-daemon not connected")
	}

	resp, err := m.vsock.SendCommand(map[string]interface{}{
		"method":    "isProcessRunning",
		"processId": processID,
	})
	if err != nil {
		return false, err
	}

	var result struct {
		Running bool `json:"running"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return false, err
	}

	return result.Running, nil
}

func (m *Manager) MountPath(processID string, subpath string, mountName string, mode string) error {
	// virtio-9p mounts need to be configured at QEMU launch time.
	// For now, return not implemented.
	return fmt.Errorf("dynamic mount not yet implemented; configure mounts before VM start")
}

func (m *Manager) ReadFile(processName string, filePath string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil || !m.vsock.IsConnected() {
		return nil, fmt.Errorf("sdk-daemon not connected")
	}

	resp, err := m.vsock.SendCommand(map[string]interface{}{
		"method": "readFile",
		"path":   filePath,
	})
	if err != nil {
		return nil, err
	}

	return []byte(resp), nil
}

func (m *Manager) InstallSdk(name string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil || !m.vsock.IsConnected() {
		return fmt.Errorf("sdk-daemon not connected")
	}

	_, err := m.vsock.SendCommand(map[string]interface{}{
		"method": "installSdk",
	})
	return err
}

func (m *Manager) AddApprovedOauthToken(name string, token string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.vsock == nil || !m.vsock.IsConnected() {
		return fmt.Errorf("sdk-daemon not connected")
	}

	_, err := m.vsock.SendCommand(map[string]interface{}{
		"method": "addApprovedOauthToken",
		"token":  token,
	})
	return err
}

// Shutdown stops any running VM, intended for use during service exit.
func (m *Manager) Shutdown() {
	log.Printf("VM manager shutting down...")
	m.StopVM("")
}

func (m *Manager) SetDebugLogging(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debug = enabled
	if m.debug {
		log.Printf("Debug logging enabled")
	}
}

func (m *Manager) SubscribeEvents(name string, callback func(event interface{})) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.subscribers = append(m.subscribers, callback)
	idx := len(m.subscribers) - 1

	cancel := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if idx < len(m.subscribers) {
			m.subscribers[idx] = nil
		}
	}

	return cancel, nil
}

func (m *Manager) GetDownloadStatus() string {
	// Check Claude Desktop's bundle directory for downloaded bundles
	entries, err := os.ReadDir(m.bundlesDir)
	if err != nil || len(entries) == 0 {
		return "NotDownloaded"
	}
	for _, entry := range entries {
		if entry.IsDir() {
			dir := filepath.Join(m.bundlesDir, entry.Name())
			// Check for raw bundle files (rootfs.vhdx, vmlinuz, initrd)
			if _, err := os.Stat(filepath.Join(dir, "rootfs.vhdx")); err == nil {
				return "Ready"
			}
			// Check for converted bundle (rootfs.qcow2)
			if _, err := os.Stat(filepath.Join(dir, "rootfs.qcow2")); err == nil {
				return "Ready"
			}
		}
	}
	return "NotDownloaded"
}

func (m *Manager) SendGuestResponse(id string, resultJSON string, errMsg string) error {
	// TODO: forward response to VM guest via vsock when VM mode is active
	if m.debug {
		log.Printf("[vm] sendGuestResponse id=%s (stub)", id)
	}
	return nil
}

func (m *Manager) emitEvent(event interface{}) {
	for _, cb := range m.subscribers {
		if cb != nil {
			go cb(event)
		}
	}
}

func (m *Manager) findLatestBundle() (string, error) {
	// Look in Claude Desktop's bundle directory
	entries, err := os.ReadDir(m.bundlesDir)
	if err != nil {
		return "", fmt.Errorf("reading bundles dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			dir := filepath.Join(m.bundlesDir, entry.Name())
			// Check for raw files (downloaded by Claude Desktop)
			if _, err := os.Stat(filepath.Join(dir, "rootfs.vhdx")); err == nil {
				return dir, nil
			}
			// Check for converted qcow2 (already prepared)
			if _, err := os.Stat(filepath.Join(dir, "rootfs.qcow2")); err == nil {
				return dir, nil
			}
			// Check for compressed files
			if _, err := os.Stat(filepath.Join(dir, "rootfs.vhdx.zst")); err == nil {
				return dir, nil
			}
		}
	}

	return "", fmt.Errorf("no bundles found in %s", m.bundlesDir)
}
