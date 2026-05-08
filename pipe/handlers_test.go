package pipe

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

type recordingBackend struct {
	startName       string
	startBundlePath string
	touches         int
}

func (b *recordingBackend) Configure(memoryMB int, cpuCount int) error { return nil }
func (b *recordingBackend) CreateVM(name string) error                 { return nil }
func (b *recordingBackend) StartVM(name string, bundlePath string, memoryGB int) error {
	b.startName = name
	b.startBundlePath = bundlePath
	return nil
}
func (b *recordingBackend) StopVM(name string) error { return nil }
func (b *recordingBackend) IsRunning(name string) (bool, error) {
	return false, nil
}
func (b *recordingBackend) IsGuestConnected(name string) (bool, error) {
	return false, nil
}
func (b *recordingBackend) Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]MountSpec, rawParams []byte) (string, error) {
	return "", nil
}
func (b *recordingBackend) Kill(processID string, signal string) error { return nil }
func (b *recordingBackend) WriteStdin(processID string, data []byte) error {
	return nil
}
func (b *recordingBackend) IsProcessRunning(processID string) (bool, int, error) {
	return false, 0, nil
}
func (b *recordingBackend) MountPath(processID string, subpath string, mountName string, mode string) error {
	return nil
}
func (b *recordingBackend) ReadFile(processName string, filePath string) ([]byte, error) {
	return nil, nil
}
func (b *recordingBackend) InstallSdk(sdkSubpath string, version string) error { return nil }
func (b *recordingBackend) AddApprovedOauthToken(token string) error {
	return nil
}
func (b *recordingBackend) SetDebugLogging(enabled bool) {}
func (b *recordingBackend) SubscribeEvents(name string, callback func(event interface{})) (func(), error) {
	return func() {}, nil
}
func (b *recordingBackend) GetDownloadStatus() string { return "Ready" }
func (b *recordingBackend) GetSessionsDiskInfo(lowWaterBytes int64) (SessionsDiskInfo, error) {
	return SessionsDiskInfo{}, nil
}
func (b *recordingBackend) DeleteSessionDirs(names []string) (DeleteSessionDirsResult, error) {
	return DeleteSessionDirsResult{}, nil
}
func (b *recordingBackend) CreateDiskImage(diskName string, sizeGiB int) error { return nil }
func (b *recordingBackend) SendGuestResponse(id string, resultJSON string, errMsg string) error {
	return nil
}
func (b *recordingBackend) Touch() { b.touches++ }

func TestHandleStartVMPassesExactBundlePath(t *testing.T) {
	backend := &recordingBackend{}
	handler := NewHandler(backend, false)
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	bundlePath := filepath.Join("/tmp", "vm_bundles", "bundle-2026-04-21")
	payload, err := json.Marshal(Request{
		Method: "startVM",
		ID:     7,
		Params: mustRawJSON(t, map[string]interface{}{
			"bundlePath": bundlePath,
		}),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = server.Close() }()
		handler.Handle(server, payload)
	}()

	rawResp, err := ReadMessage(client)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	<-done

	var resp Response
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("response success=false: %s", resp.Error)
	}
	if backend.startBundlePath != bundlePath {
		t.Fatalf("start bundlePath = %q, want %q", backend.startBundlePath, bundlePath)
	}
	if backend.startName != filepath.Base(bundlePath) {
		t.Fatalf("start name = %q, want %q", backend.startName, filepath.Base(bundlePath))
	}
	if backend.touches != 1 {
		t.Fatalf("Touch count = %d, want 1", backend.touches)
	}
}

func mustRawJSON(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal raw json: %v", err)
	}
	return raw
}
