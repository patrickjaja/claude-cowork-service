package vm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickjaja/claude-cowork-service/process"
)

func TestRunPendingSdkInstallKeepsRequestWhenGuestDisconnected(t *testing.T) {
	b := NewKvmBackend("", false)
	pending := &pendingSdkInstall{sdkSubpath: "sdk/bin", version: "1.2.3"}
	b.pendingSdkInstall = pending

	b.runPendingSdkInstall()

	if b.pendingSdkInstall != pending {
		t.Fatalf("pending install was cleared without a successful guest forward")
	}
}

func TestRunPendingSdkInstallClearsRequestAfterSuccess(t *testing.T) {
	bridge, reader, writer := newConnectedTestBridge(t)
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()

	b := NewKvmBackend("", false)
	b.bridge = bridge
	b.pendingSdkInstall = &pendingSdkInstall{sdkSubpath: "sdk/bin", version: "1.2.3"}

	done := make(chan struct{})
	go func() {
		b.runPendingSdkInstall()
		close(done)
	}()

	req := readGuestRequest(t, reader)
	var method string
	if err := json.Unmarshal(req["method"], &method); err != nil {
		t.Fatalf("unmarshal method: %v", err)
	}
	if method != "installSdk" {
		t.Fatalf("forwarded method = %q, want installSdk", method)
	}

	bridge.handleMessage(mustJSON(t, map[string]interface{}{
		"type":   "response",
		"id":     normalizeID(req["id"]),
		"result": map[string]bool{"ok": true},
	}))
	<-done

	if b.pendingSdkInstall != nil {
		t.Fatalf("pending install was not cleared after a successful guest forward")
	}
}

func TestEmitRemovesExitedProcessesFromRunningState(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		event interface{}
	}{
		{
			name:  "guest exit map",
			id:    "guest-proc",
			event: map[string]interface{}{"type": "exit", "id": "guest-proc", "exitCode": float64(0)},
		},
		{
			name:  "local exit struct",
			id:    "local-proc",
			event: process.NewExitEvent("local-proc", 1),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewKvmBackend("", false)
			b.processes[tc.id] = struct{}{}

			b.emit(tc.event)

			running, _, err := b.IsProcessRunning(tc.id)
			if err != nil {
				t.Fatalf("IsProcessRunning: %v", err)
			}
			if running {
				t.Fatalf("process %q still marked running after exit event", tc.id)
			}
		})
	}
}

func TestFindBundleUsesRequestedPath(t *testing.T) {
	bundlesDir := t.TempDir()
	oldBundle := filepath.Join(bundlesDir, "a-old")
	requestedBundle := filepath.Join(bundlesDir, "z-requested")
	for _, dir := range []string{oldBundle, requestedBundle} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rootfs.qcow2"), []byte("qcow2"), 0o644); err != nil {
			t.Fatalf("WriteFile rootfs.qcow2 in %s: %v", dir, err)
		}
	}

	b := NewKvmBackend(bundlesDir, false)
	got, err := b.findBundle(requestedBundle)
	if err != nil {
		t.Fatalf("findBundle: %v", err)
	}
	if got != requestedBundle {
		t.Fatalf("findBundle returned %q, want %q", got, requestedBundle)
	}
}

func TestIsRunningClearsStaleStartedState(t *testing.T) {
	sessionRoot := t.TempDir()
	sessionDir := filepath.Join(sessionRoot, "session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}

	b := NewKvmBackend("", false)
	watchdogStop := make(chan struct{})
	b.started = true
	b.qemu = &qemuInstance{running: false, exitedCh: make(chan struct{})}
	b.sessionDir = sessionDir
	b.sessionName = "session-123"
	b.bundleDir = "/tmp/requested-bundle"
	b.watchdogStop = watchdogStop

	running, err := b.IsRunning("")
	if err != nil {
		t.Fatalf("IsRunning: %v", err)
	}
	if running {
		t.Fatalf("IsRunning returned true for dead QEMU")
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.started {
		t.Fatalf("backend still marked started after stale QEMU detection")
	}
	if b.qemu != nil {
		t.Fatalf("backend still retains dead qemu instance")
	}
	if b.sessionDir != "" {
		t.Fatalf("sessionDir not cleared: %q", b.sessionDir)
	}
	if b.sessionName != "" {
		t.Fatalf("sessionName not cleared: %q", b.sessionName)
	}
	if b.bundleDir != "" {
		t.Fatalf("bundleDir not cleared: %q", b.bundleDir)
	}
	if b.watchdogStop != nil {
		t.Fatalf("watchdogStop not cleared")
	}

	select {
	case <-watchdogStop:
	default:
		t.Fatalf("watchdog stop channel was not closed")
	}

	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("sessionDir still exists or unexpected error: %v", err)
	}
}

func newConnectedTestBridge(t *testing.T) (*GuestBridge, *os.File, *os.File) {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	bridge := NewGuestBridge(VsockGuestPort, false, func(interface{}) {})
	bridge.conn = &vsockConn{file: writer}
	bridge.connected.Store(true)
	return bridge, reader, writer
}

func readGuestRequest(t *testing.T, reader *os.File) map[string]json.RawMessage {
	t.Helper()

	raw, err := readFramed(reader)
	if err != nil {
		t.Fatalf("readFramed: %v", err)
	}

	var req map[string]json.RawMessage
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("json.Unmarshal request: %v", err)
	}
	return req
}

func mustJSON(t *testing.T, payload interface{}) []byte {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}
