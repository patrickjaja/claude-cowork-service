package vm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/patrickjaja/claude-cowork-service/logx"
	"github.com/patrickjaja/claude-cowork-service/pipe"
	"github.com/patrickjaja/claude-cowork-service/process"
)

// KvmBackend runs guest workloads inside a QEMU/KVM virtual machine sharing
// $HOME with the host over virtiofs. It implements pipe.VMBackend, and is
// the Go port of the JavaScript cowork-vm-service KvmBackend.
type KvmBackend struct {
	baseDir    string // ~/.local/share/claude-desktop/vm
	bundlesDir string // Desktop's bundle download dir
	debug      bool

	mu          sync.RWMutex
	memoryMB    int
	cpus        int
	started     bool
	starting    bool // pipeline in flight; prevents concurrent StartVM double-launch
	sessionDir  string
	sessionName string
	bundleDir   string

	qemu   *qemuInstance
	qmp    *QmpClient
	helper *VfsHelper
	bridge *GuestBridge

	// Pending state for methods called before the VM is fully up.
	pendingSdkInstall *pendingSdkInstall
	pendingSdkBind    *pendingBind

	// Process bookkeeping — existence only; stdout/stderr/exit flow via events.
	processes map[string]struct{}
	procMu    sync.Mutex

	lastActivity atomic.Int64 // unix nanos — updated by Touch()
	watchdogStop chan struct{}

	subscribers map[uint64]func(event interface{})
	nextSubID   uint64
	subMu       sync.RWMutex
}

type pendingBind struct {
	subpath string
	mode    string
}

type pendingSdkInstall struct {
	sdkSubpath string
	version    string
}

type vmRuntimeState struct {
	qemu         *qemuInstance
	qmp          *QmpClient
	helper       *VfsHelper
	bridge       *GuestBridge
	sessionDir   string
	sessionName  string
	bundleDir    string
	watchdogStop chan struct{}
}

func (p *pendingSdkInstall) forwardParams() map[string]interface{} {
	if p == nil {
		return nil
	}
	return map[string]interface{}{
		"sdkSubpath": p.sdkSubpath,
		"version":    p.version,
	}
}

// keepaliveTimeout is how long the VM may run without any RPC activity from
// Desktop before the watchdog concludes Desktop died and tears it down.
// Desktop's keepalive cadence is ~2s, so 30s tolerates a brief hiccup.
const keepaliveTimeout = 30 * time.Second

// NewKvmBackend creates a KVM backend. bundlesDir is where Claude Desktop
// drops downloaded VM bundles (typically ~/.config/Claude/vm_bundles).
func NewKvmBackend(bundlesDir string, debug bool) *KvmBackend {
	home, _ := os.UserHomeDir()
	baseDir := filepath.Join(home, ".local", "share", "claude-desktop", "vm")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		log.Printf("[kvm] MkdirAll %s: %v", baseDir, err)
	}
	return &KvmBackend{
		baseDir:     baseDir,
		bundlesDir:  bundlesDir,
		debug:       debug,
		memoryMB:    4096,
		cpus:        4,
		processes:   make(map[string]struct{}),
		subscribers: make(map[uint64]func(event interface{})),
	}
}

func (b *KvmBackend) Configure(memoryMB int, cpuCount int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if memoryMB > 0 {
		b.memoryMB = memoryMB
	}
	if cpuCount > 0 {
		b.cpus = cpuCount
	}
	if b.debug {
		log.Printf("[kvm] configure memoryMB=%d cpuCount=%d", b.memoryMB, b.cpus)
	}
	return nil
}

func (b *KvmBackend) CreateVM(name string) error {
	if b.debug {
		log.Printf("[kvm] createVM %s (no-op; done in StartVM)", name)
	}
	return nil
}

// StartVM boots the VM: prepare bundle, create session dir, launch virtiofsd
// via helper, spawn QEMU, open QMP, wait for guest bridge connection.
func (b *KvmBackend) StartVM(name string, bundlePath string, memoryGb int) error {
	var stale vmRuntimeState
	hadStale := false

	b.mu.Lock()
	if b.started {
		stale, hadStale = b.takeExitedVMStateLocked()
		if !hadStale {
			b.mu.Unlock()
			log.Printf("[kvm] startVM: already running")
			return nil
		}
	}
	if b.starting {
		b.mu.Unlock()
		log.Printf("[kvm] startVM: another start already in progress — ignoring concurrent call")
		return nil
	}
	b.starting = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.starting = false
		b.mu.Unlock()
	}()

	if hadStale {
		b.cleanupVMRuntime(stale, stale.sessionName, "[kvm] startVM: detected stale runtime after unexpected QEMU exit; cleaning up before restart")
	}

	check := CheckKvmPrerequisites()
	if !check.OK {
		b.emit(map[string]interface{}{
			"type": "startupStep", "step": "prepare_session",
			"status": "failed", "error": check.Reason,
		})
		return fmt.Errorf("KVM unavailable: %s", check.Reason)
	}

	b.emit(map[string]interface{}{
		"type": "startupStep", "step": "prepare_session", "status": "running",
	})

	bundleDir, err := b.findBundle(bundlePath)
	if err != nil {
		return fmt.Errorf("no VM bundle available: %w", err)
	}
	rootQcow2, err := ensureVHDXConverted(bundleDir, "rootfs")
	if err != nil {
		return fmt.Errorf("preparing rootfs: %w", err)
	}

	sessionID := name
	if sessionID == "" {
		sessionID = randomID()
	}
	sessionDir := filepath.Join(b.baseDir, "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return fmt.Errorf("creating session dir: %w", err)
	}
	killStalePID(sessionDir)

	// sessiondata.qcow2 lives next to the other bundle images, not per-host
	// session, mirroring upstream. The guest carves this disk into
	// per-session subdirectories internally; tearing it down on stop would
	// delete every resumable session's files.
	sessionDiskPath := filepath.Join(bundleDir, "sessiondata.qcow2")
	if err := ensureSessionDataDisk(sessionDiskPath); err != nil {
		log.Printf("[kvm] session disk creation failed: %v", err)
	}

	smolBinPath := findSmolBin([]string{bundleDir, b.baseDir})
	if smolBinPath != "" {
		log.Printf("[kvm] smol-bin attached from %s", smolBinPath)
	} else {
		log.Printf("[kvm] smol-bin not found — sdk-daemon must live in rootfs")
	}

	// Launch VFS helper in its own user + mount namespace. virtiofsd runs
	// inside it so the bind mounts it publishes to the guest become live
	// as soon as we add them.
	virtiofsSock := filepath.Join(sessionDir, "virtiofs.sock")
	stagingDir := filepath.Join(sessionDir, "virtiofs-root")
	if err := os.MkdirAll(filepath.Join(stagingDir, "shared"), 0o755); err != nil {
		return fmt.Errorf("creating virtiofs staging dir: %w", err)
	}
	helper := NewVfsHelper(stagingDir, virtiofsSock, b.debug)
	if err := helper.Start(15 * time.Second); err != nil {
		return fmt.Errorf("starting vfs helper: %w", err)
	}

	// Replay pending SDK bind queued via installSdk before startVM.
	b.mu.Lock()
	if b.pendingSdkBind != nil {
		if err := helper.Bind(b.pendingSdkBind.subpath, b.pendingSdkBind.mode); err != nil {
			log.Printf("[kvm] pending SDK bind failed: %v", err)
		}
		b.pendingSdkBind = nil
	}
	b.mu.Unlock()

	cid := b.allocateCID()
	monitorSock := filepath.Join(sessionDir, "qmp.sock")

	memGb := (b.memoryMB + 1023) / 1024
	if memoryGb > 0 {
		memGb = memoryGb
	}
	if memGb < 1 {
		memGb = 1
	}
	log.Printf("[kvm] starting VM %s with %dGB memory", name, memGb)

	b.emit(map[string]interface{}{
		"type": "startupStep", "step": "start_vm", "status": "running",
	})

	spec := qemuLaunchSpec{
		bundleDir:    bundleDir,
		sessionDir:   sessionDir,
		rootDisk:     rootQcow2,
		sessionData:  sessionDiskPath,
		smolBinPath:  smolBinPath,
		kernel:       filepath.Join(bundleDir, "vmlinuz"),
		initrd:       filepath.Join(bundleDir, "initrd"),
		monitorSock:  monitorSock,
		virtiofsSock: virtiofsSock,
		cid:          cid,
		memoryGB:     memGb,
		cpus:         b.cpus,
	}
	qemu, err := startQEMU(spec, b.debug)
	if err != nil {
		helper.Stop()
		return err
	}

	// Open QMP (best effort — continue if it fails, we only use it for
	// graceful shutdown).
	qmp, err := DialQMP(monitorSock, 30*time.Second)
	if err != nil {
		log.Printf("[kvm] QMP connect failed: %v (shutdown will fall back to SIGTERM)", err)
		qmp = nil
	} else {
		log.Printf("[kvm] QMP connected at %s", monitorSock)
	}

	// Bridge: listen on vsock for guest sdk-daemon inbound connection.
	bridge := NewGuestBridge(VsockGuestPort, b.debug, b.emit)
	guestReady := make(chan struct{})
	if err := bridge.Listen(func() { close(guestReady) }); err != nil {
		log.Printf("[kvm] bridge.Listen failed: %v — tearing down VM", err)
		qemu.Shutdown(qmp)
		if qmp != nil {
			if cerr := qmp.Close(); cerr != nil && b.debug {
				log.Printf("[kvm] qmp close (bridge-listen teardown): %v", cerr)
			}
		}
		helper.Stop()
		return fmt.Errorf("bridge listen: %w", err)
	}
	log.Printf("[kvm] bridge listening on vsock port %d", VsockGuestPort)

	b.mu.Lock()
	b.started = true
	b.sessionDir = sessionDir
	b.sessionName = sessionID
	b.bundleDir = bundleDir
	b.qemu = qemu
	b.qmp = qmp
	b.helper = helper
	b.bridge = bridge
	b.watchdogStop = make(chan struct{})
	b.mu.Unlock()

	// Wait up to 90s for guest to connect. Return success either way so
	// Desktop's UI can proceed; actual spawns will fail loud if needed.
	b.emit(map[string]interface{}{
		"type": "startupStep", "step": "wait_for_guest", "status": "running",
	})
	select {
	case <-guestReady:
		b.emit(map[string]interface{}{
			"type": "startupStep", "step": "wait_for_guest", "status": "completed",
		})
		// Install queued SDK now that the guest is up.
		b.runPendingSdkInstall()
	case <-time.After(90 * time.Second):
		log.Printf("[kvm] guest readiness timeout")
		b.emit(map[string]interface{}{
			"type": "startupStep", "step": "wait_for_guest", "status": "failed",
		})
	}

	b.mu.RLock()
	watchdogStop := b.watchdogStop
	startWatchdog := b.started && watchdogStop != nil
	b.mu.RUnlock()
	if startWatchdog {
		b.lastActivity.Store(time.Now().UnixNano())
		go b.watchdogLoop(watchdogStop)
	}

	b.emit(map[string]interface{}{"type": "vmStarted", "name": name})
	return nil
}

// StopVM shuts down the VM gracefully, tears down helper + bridge, removes
// the session directory.
func (b *KvmBackend) StopVM(name string) error {
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		log.Printf("[kvm] stopVM %q: already stopped (no-op)", name)
		return nil
	}
	state := b.takeVMRuntimeLocked()
	b.mu.Unlock()

	log.Printf("[kvm] stopVM %s", name)
	b.cleanupVMRuntime(state, name, "")
	return nil
}

func (b *KvmBackend) IsRunning(name string) (bool, error) {
	var stale vmRuntimeState
	hadStale := false

	b.mu.Lock()
	stale, hadStale = b.takeExitedVMStateLocked()
	qemu := b.qemu
	b.mu.Unlock()
	if hadStale {
		b.cleanupVMRuntime(stale, stale.sessionName, "[kvm] isRunning: detected stale runtime after unexpected QEMU exit; cleaning up")
		return false, nil
	}
	if qemu == nil {
		return false, nil
	}
	return qemu.IsRunning(), nil
}

func (b *KvmBackend) IsGuestConnected(name string) (bool, error) {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil {
		return false, nil
	}
	return bridge.IsConnected(), nil
}

// Spawn binds any new additionalMounts into the virtiofs share, then
// forwards the spawn request to the guest sdk-daemon.
func (b *KvmBackend) Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]pipe.MountSpec, rawParams []byte) (string, error) {
	b.mu.RLock()
	helper := b.helper
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil {
		return "", fmt.Errorf("VM not started")
	}

	// Bind any additionalMounts the helper doesn't know about yet.
	if helper != nil {
		for mountName, mount := range mounts {
			if mount.Path == "" {
				continue
			}
			mode := mount.Mode
			if mode == "" {
				mode = "rw"
			}
			if err := helper.Bind(mount.Path, mode); err != nil {
				log.Printf("[kvm] spawn bind failed for %s=%s (%s): %v", mountName, mount.Path, mode, err)
				return "", fmt.Errorf("vfs bind for %s failed: %w", mountName, err)
			}
		}
	}

	b.runPendingSdkInstall()

	// Desktop's Linux patches rewrite `pathToClaudeCodeExecutable` to a
	// HOST-local claude path (e.g. /home/$USER/.local/bin/claude). That's
	// right for the native backend but wrong for KVM — the guest VM
	// doesn't have that path. Rewrite any host-style claude path back to
	// the canonical guest install location the macOS/Windows client uses.
	if strings.HasSuffix(cmd, "/claude") && cmd != "/usr/local/bin/claude" {
		log.Printf("[kvm] rewriting spawn command %s -> /usr/local/bin/claude (guest path)", cmd)
		cmd = "/usr/local/bin/claude"
	}
	log.Printf("[kvm] spawn forwarding to guest: id=%s command=%s", id, cmd)

	// Forward Desktop's raw params to the guest so sdk-daemon sees every
	// field it expects (isResume, allowedDomains, sharedCwdPath, oneShot,
	// mountSkeletonHome, mountConda, additionalMounts.mode, …). Rebuilding
	// the object from the handler's decoded struct drops anything the
	// struct doesn't name, and the guest rejects the request. Parse the
	// original JSON, override `command` with the rewrite, and forward.
	var spawnParams map[string]interface{}
	if len(rawParams) > 0 {
		if err := json.Unmarshal(rawParams, &spawnParams); err != nil {
			log.Printf("[kvm] spawn: could not parse raw params: %v", err)
			spawnParams = nil
		}
	}
	if spawnParams == nil {
		spawnParams = map[string]interface{}{
			"id": id, "name": name, "args": args, "env": env, "cwd": cwd,
			"additionalMounts": toAdditionalMounts(mounts),
		}
	}
	spawnParams["command"] = cmd
	resp, err := bridge.Forward("spawn", spawnParams)
	if err != nil {
		log.Printf("[kvm] spawn forward failed: %v", err)
		b.emit(process.NewStderrEvent(id,
			fmt.Sprintf("Error: Failed to spawn in VM: %v\n", err)))
		b.emit(process.NewExitEvent(id, 1))
		return id, nil
	}
	log.Printf("[kvm] spawn ack from guest: id=%s resp=%s", id, logx.Trunc(string(resp)))

	b.procMu.Lock()
	b.processes[id] = struct{}{}
	b.procMu.Unlock()
	return id, nil
}

func (b *KvmBackend) Kill(processID string, signal string) error {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil {
		return nil
	}
	_, err := bridge.Forward("kill", map[string]interface{}{
		"id": processID, "signal": signal,
	})
	if err != nil && b.debug {
		log.Printf("[kvm] kill forward failed: %v", err)
	}
	b.procMu.Lock()
	delete(b.processes, processID)
	b.procMu.Unlock()
	return nil
}

func (b *KvmBackend) WriteStdin(processID string, data []byte) error {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil || !bridge.IsConnected() {
		return nil
	}
	// Guest treats stdin as a notification (fire-and-forget).
	return bridge.Notify("stdin", map[string]interface{}{
		"id":   processID,
		"data": string(data),
	})
}

func (b *KvmBackend) IsProcessRunning(processID string) (bool, int, error) {
	b.procMu.Lock()
	_, ok := b.processes[processID]
	b.procMu.Unlock()
	return ok, 0, nil
}

// MountPath adds a bind mount into the virtiofs staging area. The guest
// sdk-daemon maps it to /mnt/.virtiofs-root/shared/<subpath>.
func (b *KvmBackend) MountPath(processID string, subpath string, mountName string, mode string) error {
	b.mu.RLock()
	helper := b.helper
	b.mu.RUnlock()
	if b.debug {
		log.Printf("[kvm] mountPath %s=%s (%s)", mountName, subpath, mode)
	}
	if subpath == "" {
		return nil
	}
	if helper == nil {
		// VM not started yet — should be rare; remember as pending SDK
		// bind if mode is rw.
		return fmt.Errorf("vfs helper not started")
	}
	return helper.Bind(subpath, mode)
}

// ReadFile forwards to the guest when connected, else falls back to host
// read with a $HOME containment check.
func (b *KvmBackend) ReadFile(processName string, filePath string) ([]byte, error) {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()

	if bridge != nil && bridge.IsConnected() {
		resp, err := bridge.Forward("readFile", map[string]interface{}{
			"processName": processName, "filePath": filePath,
		})
		if err == nil {
			var r struct {
				Content string `json:"content"`
				Error   string `json:"error"`
			}
			if json.Unmarshal(resp, &r) == nil {
				if r.Error != "" {
					return nil, fmt.Errorf("%s", r.Error)
				}
				return []byte(r.Content), nil
			}
		}
		if b.debug {
			log.Printf("[kvm] guest readFile failed, trying host: %v", err)
		}
	}

	// Host fallback: translate virtiofs guest paths back to host absolutes.
	var resolved string
	if strings.HasPrefix(filePath, VFSGuestSharedPrefix+"/") {
		rel := strings.TrimPrefix(filePath, VFSGuestSharedPrefix+"/")
		abs, err := hostAbsFromShared(rel)
		if err != nil {
			return nil, fmt.Errorf("cannot translate %s: %w", filePath, err)
		}
		resolved = abs
	} else {
		resolved = filepath.Clean(filePath)
	}
	home, _ := os.UserHomeDir()
	if resolved != home && !strings.HasPrefix(resolved, home+string(filepath.Separator)) {
		return nil, fmt.Errorf("access denied: path outside home directory")
	}
	return os.ReadFile(resolved)
}

// InstallSdk binds the SDK install dir rw into the virtiofs share so the
// guest can download the binary there, then forwards {sdkSubpath, version}
// to the guest sdk-daemon. If the helper or the guest isn't up yet, queue
// the work so it runs before the first spawn.
func (b *KvmBackend) InstallSdk(sdkSubpath string, version string) error {
	log.Printf("[kvm] installSdk %s@%s", sdkSubpath, version)

	pending := &pendingSdkInstall{
		sdkSubpath: sdkSubpath,
		version:    version,
	}

	b.mu.Lock()
	helper := b.helper
	bridge := b.bridge
	helperReady := helper != nil
	b.pendingSdkInstall = pending
	if sdkSubpath != "" && !helperReady {
		b.pendingSdkBind = &pendingBind{subpath: sdkSubpath, mode: "rw"}
	}
	b.mu.Unlock()

	// Bind now if the helper is already up; replayed by StartVM otherwise.
	if sdkSubpath != "" && helperReady {
		if err := helper.Bind(sdkSubpath, "rw"); err != nil {
			log.Printf("[kvm] installSdk bind failed: %v", err)
		}
	}

	if bridge != nil && bridge.IsConnected() {
		b.runPendingSdkInstall()
	} else {
		log.Printf("[kvm] installSdk queued — will forward after guest connects")
	}
	return nil
}

func (b *KvmBackend) runPendingSdkInstall() {
	b.mu.RLock()
	pending := b.pendingSdkInstall
	bridge := b.bridge
	b.mu.RUnlock()
	if pending == nil || bridge == nil || !bridge.IsConnected() {
		return
	}
	resp, err := bridge.Forward("installSdk", pending.forwardParams())
	if err != nil {
		log.Printf("[kvm] installSdk forward failed: %v", err)
		return
	}
	b.mu.Lock()
	if b.pendingSdkInstall == pending {
		b.pendingSdkInstall = nil
	}
	b.mu.Unlock()
	log.Printf("[kvm] installSdk ack from guest: resp=%s", logx.Trunc(string(resp)))
}

func (b *KvmBackend) AddApprovedOauthToken(token string) error {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil || !bridge.IsConnected() {
		return nil
	}
	if _, err := bridge.Forward("addApprovedOauthToken",
		map[string]interface{}{"token": token}); err != nil {
		log.Printf("[kvm] oauth forward failed: %v", err)
	}
	return nil
}

func (b *KvmBackend) SetDebugLogging(enabled bool) {
	b.mu.Lock()
	b.debug = enabled
	b.mu.Unlock()
	if enabled {
		log.Printf("[kvm] debug logging enabled")
	}
}

func (b *KvmBackend) SubscribeEvents(name string, callback func(event interface{})) (func(), error) {
	b.subMu.Lock()
	b.nextSubID++
	id := b.nextSubID
	b.subscribers[id] = callback
	b.subMu.Unlock()

	cancel := func() {
		b.subMu.Lock()
		delete(b.subscribers, id)
		b.subMu.Unlock()
	}
	return cancel, nil
}

func (b *KvmBackend) GetDownloadStatus() string {
	if _, err := os.Stat(b.bundlesDir); err != nil {
		return "NotDownloaded"
	}
	entries, err := os.ReadDir(b.bundlesDir)
	if err != nil {
		return "NotDownloaded"
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(b.bundlesDir, e.Name())
		for _, f := range []string{"rootfs.qcow2", "rootfs.vhdx"} {
			if _, err := os.Stat(filepath.Join(d, f)); err == nil {
				return "Ready"
			}
		}
	}
	return "NotDownloaded"
}

func (b *KvmBackend) GetSessionsDiskInfo(lowWaterBytes int64) (pipe.SessionsDiskInfo, error) {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil || !bridge.IsConnected() {
		return pipe.SessionsDiskInfo{}, fmt.Errorf("guest not connected")
	}
	resp, err := bridge.Forward("getSessionsDiskInfo", map[string]interface{}{
		"lowWaterBytes": lowWaterBytes,
	})
	if err != nil {
		return pipe.SessionsDiskInfo{}, err
	}
	var info pipe.SessionsDiskInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		return pipe.SessionsDiskInfo{}, fmt.Errorf("parsing getSessionsDiskInfo response: %w", err)
	}
	return info, nil
}

func (b *KvmBackend) DeleteSessionDirs(names []string) (pipe.DeleteSessionDirsResult, error) {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil || !bridge.IsConnected() {
		return pipe.DeleteSessionDirsResult{}, fmt.Errorf("guest not connected")
	}
	resp, err := bridge.Forward("deleteSessionDirs", map[string]interface{}{
		"names": names,
	})
	if err != nil {
		return pipe.DeleteSessionDirsResult{}, err
	}
	var result pipe.DeleteSessionDirsResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return pipe.DeleteSessionDirsResult{}, fmt.Errorf("parsing deleteSessionDirs response: %w", err)
	}
	if result.Errors == nil {
		result.Errors = map[string]string{}
	}
	return result, nil
}

func (b *KvmBackend) CreateDiskImage(diskName string, sizeGiB int) error {
	if b.debug {
		log.Printf("[kvm] createDiskImage diskName=%q sizeGiB=%d (no-op)", diskName, sizeGiB)
	}
	return nil
}

func (b *KvmBackend) SendGuestResponse(id string, resultJSON string, errMsg string) error {
	b.mu.RLock()
	bridge := b.bridge
	b.mu.RUnlock()
	if bridge == nil || !bridge.IsConnected() {
		return nil
	}
	// Forward as a request-style reply the guest can route to its own
	// pending handler. The guest's protocol treats responses uniformly:
	// {type:"response", id, result|error}.
	var result json.RawMessage
	if resultJSON != "" {
		result = json.RawMessage(resultJSON)
	}
	payload := map[string]interface{}{
		"type": "response", "id": id,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	} else {
		payload["result"] = result
	}
	return bridge.Notify("guestResponse", payload)
}

// Shutdown is called on process exit. It performs a best-effort StopVM.
func (b *KvmBackend) Shutdown() {
	log.Printf("[kvm] shutting down")
	if err := b.StopVM(""); err != nil && b.debug {
		log.Printf("[kvm] StopVM on shutdown: %v", err)
	}
}

// Touch records fresh RPC activity. Used by the keepalive watchdog to tell
// whether Desktop has gone silent (and therefore died without sending stopVM).
func (b *KvmBackend) Touch() {
	b.lastActivity.Store(time.Now().UnixNano())
}

// watchdogLoop runs while the VM is up and tears it down if no RPC activity
// has been seen for keepaliveTimeout. Desktop pings isProcessRunning every
// ~2s, so a 30s silence means Desktop crashed or was killed.
func (b *KvmBackend) watchdogLoop(stop <-chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			last := b.lastActivity.Load()
			if last == 0 {
				continue
			}
			if time.Since(time.Unix(0, last)) < keepaliveTimeout {
				continue
			}
			log.Printf("[kvm] watchdog: no RPC activity for %s — Desktop presumed dead, stopping VM",
				keepaliveTimeout)
			go func() {
				if err := b.StopVM(""); err != nil {
					log.Printf("[kvm] watchdog StopVM: %v", err)
				}
			}()
			return
		}
	}
}

func (b *KvmBackend) emit(event interface{}) {
	b.noteProcessEvent(event)
	b.subMu.RLock()
	subs := make([]func(event interface{}), 0, len(b.subscribers))
	for _, s := range b.subscribers {
		subs = append(subs, s)
	}
	b.subMu.RUnlock()
	for _, cb := range subs {
		go cb(event)
	}
}

func (b *KvmBackend) noteProcessEvent(event interface{}) {
	processID, exited := exitedProcessID(event)
	if !exited || processID == "" {
		return
	}
	b.procMu.Lock()
	delete(b.processes, processID)
	b.procMu.Unlock()
}

func exitedProcessID(event interface{}) (string, bool) {
	switch ev := event.(type) {
	case process.ExitEvent:
		return ev.ProcessID, true
	case *process.ExitEvent:
		if ev == nil {
			return "", false
		}
		return ev.ProcessID, true
	case map[string]interface{}:
		typ, _ := ev["type"].(string)
		if typ != "exit" {
			return "", false
		}
		return eventProcessID(ev["id"]), true
	default:
		return "", false
	}
}

func eventProcessID(raw interface{}) string {
	switch id := raw.(type) {
	case string:
		return id
	case float64:
		return strconv.FormatFloat(id, 'f', -1, 64)
	default:
		return ""
	}
}

// allocateCID picks an unused vsock CID, persisting the counter between runs.
// CIDs 0-2 are reserved by the kernel. The read-modify-write is serialized
// with flock(LOCK_EX) on the counter file so two concurrent daemons (or two
// concurrent StartVM calls in the same process) cannot hand out the same CID
// — the kernel rejects duplicate vhost-vsock guest-cid bindings and the
// resulting QEMU exit surfaces as a generic "exited immediately" error that
// points operators at disk/KVM instead of the real cause.
func (b *KvmBackend) allocateCID() uint32 {
	cidFile := filepath.Join(b.baseDir, ".next_cid")
	const defaultCID = uint32(3)

	f, err := os.OpenFile(cidFile, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		log.Printf("[kvm] open CID counter %s: %v — falling back to CID %d", cidFile, err, defaultCID)
		return defaultCID
	}
	defer func() { _ = f.Close() }()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		log.Printf("[kvm] flock CID counter: %v — falling back to CID %d", err, defaultCID)
		return defaultCID
	}
	// LOCK_UN happens implicitly on Close; no explicit unlock needed.

	cid := defaultCID
	var buf [32]byte
	if n, _ := f.ReadAt(buf[:], 0); n > 0 {
		var parsed uint32
		if _, err := fmt.Sscanf(string(buf[:n]), "%d", &parsed); err == nil && parsed >= 3 {
			cid = parsed
		}
	}
	next := cid + 1
	if next >= 65535 {
		next = 3
	}
	if err := f.Truncate(0); err != nil {
		log.Printf("[kvm] truncate CID counter: %v", err)
		return cid
	}
	if _, err := f.WriteAt([]byte(fmt.Sprintf("%d", next)), 0); err != nil {
		log.Printf("[kvm] write CID counter: %v", err)
	}
	return cid
}

// findBundle returns the first directory under bundlesDir that contains a
// usable rootfs. Falls back to baseDir if Desktop hasn't placed anything in
// bundlesDir yet.
func (b *KvmBackend) findBundle(bundlePath string) (string, error) {
	if bundlePath != "" {
		requested := filepath.Clean(bundlePath)
		if info, err := os.Stat(requested); err == nil && !info.IsDir() {
			requested = filepath.Dir(requested)
		}
		if hasUsableRootfs(requested) {
			return requested, nil
		}
		return "", fmt.Errorf("requested bundle %s has no rootfs.qcow2 or rootfs.vhdx", requested)
	}

	candidates := []string{b.bundlesDir, b.baseDir}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if hasUsableRootfs(dir) {
			return dir, nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			d := filepath.Join(dir, e.Name())
			if hasUsableRootfs(d) {
				return d, nil
			}
		}
	}
	return "", fmt.Errorf("no rootfs in %s or %s", b.bundlesDir, b.baseDir)
}

func hasUsableRootfs(dir string) bool {
	for _, f := range []string{"rootfs.qcow2", "rootfs.vhdx"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

func (b *KvmBackend) takeExitedVMStateLocked() (vmRuntimeState, bool) {
	if !b.started {
		return vmRuntimeState{}, false
	}
	if b.qemu != nil && b.qemu.IsRunning() {
		return vmRuntimeState{}, false
	}
	return b.takeVMRuntimeLocked(), true
}

func (b *KvmBackend) takeVMRuntimeLocked() vmRuntimeState {
	state := vmRuntimeState{
		qemu:         b.qemu,
		qmp:          b.qmp,
		helper:       b.helper,
		bridge:       b.bridge,
		sessionDir:   b.sessionDir,
		sessionName:  b.sessionName,
		bundleDir:    b.bundleDir,
		watchdogStop: b.watchdogStop,
	}
	b.started = false
	b.qemu = nil
	b.qmp = nil
	b.helper = nil
	b.bridge = nil
	b.sessionDir = ""
	b.sessionName = ""
	b.bundleDir = ""
	b.watchdogStop = nil
	return state
}

func (b *KvmBackend) cleanupVMRuntime(state vmRuntimeState, name string, reason string) {
	if reason != "" {
		log.Printf("%s", reason)
	}
	if state.watchdogStop != nil {
		close(state.watchdogStop)
	}
	if state.bridge != nil {
		state.bridge.Close()
	}
	if state.qemu != nil {
		state.qemu.Shutdown(state.qmp)
	}
	if state.qmp != nil {
		if err := state.qmp.Close(); err != nil && b.debug {
			log.Printf("[kvm] qmp close: %v", err)
		}
	}
	if state.helper != nil {
		state.helper.Stop()
	}
	if state.sessionDir != "" {
		if err := os.RemoveAll(state.sessionDir); err != nil {
			log.Printf("[kvm] session cleanup error: %v", err)
		}
	}

	b.procMu.Lock()
	b.processes = make(map[string]struct{})
	b.procMu.Unlock()

	stopName := name
	if stopName == "" {
		stopName = state.sessionName
	}
	b.emit(map[string]interface{}{
		"type": "networkStatus", "status": "disconnected",
	})
	b.emit(map[string]interface{}{"type": "vmStopped", "name": stopName})
}

func randomID() string {
	var buf [8]byte
	rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// toAdditionalMounts reconstructs the {mountName: {path, mode}} shape the
// guest expects from the flat (name -> subpath) map the handler passes.
func toAdditionalMounts(mounts map[string]pipe.MountSpec) map[string]map[string]string {
	out := make(map[string]map[string]string, len(mounts))
	for name, mount := range mounts {
		entry := map[string]string{"path": mount.Path}
		if mount.Mode != "" {
			entry["mode"] = mount.Mode
		}
		out[name] = entry
	}
	return out
}
