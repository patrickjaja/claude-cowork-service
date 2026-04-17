package pipe

import (
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/patrickjaja/claude-cowork-service/logx"
)

// Handler dispatches RPC methods to the VM backend.
type Handler struct {
	backend VMBackend
	debug   bool
}

// NewHandler creates a new RPC handler.
func NewHandler(backend VMBackend, debug bool) *Handler {
	return &Handler{backend: backend, debug: debug}
}

// Handle parses and dispatches an RPC request.
func (h *Handler) Handle(conn net.Conn, payload []byte) {
	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		logx.Debug("Invalid JSON: %v", err)
		WriteError(conn, nil, -32700, "Parse error")
		return
	}

	h.backend.Touch()

	if req.Method != "isGuestConnected" && req.Method != "isProcessRunning" {
		logx.Debug("RPC: %s (id=%v) params: %s", req.Method, req.ID, logx.Trunc(string(req.Params)))
	}

	switch req.Method {
	case "configure":
		h.handleConfigure(conn, req)
	case "createVM":
		h.handleCreateVM(conn, req)
	case "startVM":
		h.handleStartVM(conn, req)
	case "stopVM":
		h.handleStopVM(conn, req)
	case "isRunning":
		h.handleIsRunning(conn, req)
	case "isGuestConnected":
		h.handleIsGuestConnected(conn, req)
	case "spawn":
		h.handleSpawn(conn, req)
	case "kill":
		h.handleKill(conn, req)
	case "writeStdin":
		h.handleWriteStdin(conn, req)
	case "isProcessRunning":
		h.handleIsProcessRunning(conn, req)
	case "mountPath":
		h.handleMountPath(conn, req)
	case "readFile":
		h.handleReadFile(conn, req)
	case "installSdk":
		h.handleInstallSdk(conn, req)
	case "addApprovedOauthToken":
		h.handleAddApprovedOauthToken(conn, req)
	case "setDebugLogging":
		h.handleSetDebugLogging(conn, req)
	case "isDebugLoggingEnabled":
		h.handleIsDebugLoggingEnabled(conn, req)
	case "subscribeEvents":
		h.handleSubscribeEvents(conn, req)
	case "getDownloadStatus":
		h.handleGetDownloadStatus(conn, req)
	case "getSessionsDiskInfo":
		h.handleGetSessionsDiskInfo(conn, req)
	case "deleteSessionDirs":
		h.handleDeleteSessionDirs(conn, req)
	case "createDiskImage":
		h.handleCreateDiskImage(conn, req)
	case "sendGuestResponse":
		h.handleSendGuestResponse(conn, req)
	default:
		logx.Debug("RPC: unknown method %q — returning success (passthrough)", req.Method)
		WriteResponse(conn, req.ID, nil)
	}
}

// Parameter types for RPC methods

type configureParams struct {
	MemoryMB int `json:"memoryMB"`
	CPUCount int `json:"cpuCount"`
}

type vmNameParams struct {
	Name string `json:"name"`
}

type createVMParams struct {
	Name       string `json:"name"`
	BundlePath string `json:"bundlePath"`
	DiskSizeGB int    `json:"diskSizeGB"`
}

type startVMParams struct {
	Name       string `json:"name"`
	BundlePath string `json:"bundlePath"`
	MemoryGB   int    `json:"memoryGB"`
}

type killParams struct {
	ProcessID string `json:"id"`
	Signal    string `json:"signal"`
}

type spawnParams struct {
	Name              string               `json:"name"`
	ID                string               `json:"id"`
	Cmd               string               `json:"command"`
	Args              []string             `json:"args"`
	Env               map[string]string    `json:"env"`
	Cwd               string               `json:"cwd"`
	AdditionalMounts  map[string]MountSpec `json:"additionalMounts"`
	IsResume          bool                 `json:"isResume"`
	AllowedDomains    []string             `json:"allowedDomains"`
	OneShot           bool                 `json:"oneShot"`
	MountSkeletonHome bool                 `json:"mountSkeletonHome"`
	MountConda        string               `json:"mountConda"`
}

type getSessionsDiskInfoParams struct {
	LowWaterBytes int64 `json:"lowWaterBytes"`
}

type deleteSessionDirsParams struct {
	Names []string `json:"names"`
}

type createDiskImageParams struct {
	DiskName string `json:"diskName"`
	SizeGiB  int    `json:"sizeGiB"`
}

type processIDParams struct {
	ProcessID string `json:"id"`
}

type writeStdinParams struct {
	ProcessID string `json:"id"`
	Data      string `json:"data"`
}

type mountPathParams struct {
	ProcessID string `json:"processId"`
	Subpath   string `json:"subpath"`
	MountName string `json:"mountName"`
	Mode      string `json:"mode"`
}

type readFileParams struct {
	ProcessName string `json:"processName"`
	FilePath    string `json:"filePath"`
}

type oauthTokenParams struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

type installSdkParams struct {
	SdkSubpath string `json:"sdkSubpath"`
	Version    string `json:"version"`
}

type debugLoggingParams struct {
	Enabled bool `json:"enabled"`
}

type sendGuestResponseParams struct {
	ID         string `json:"id"`
	ResultJSON string `json:"resultJson"`
	Error      string `json:"error"`
}

func (h *Handler) handleConfigure(conn net.Conn, req Request) {
	var p configureParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if err := h.backend.Configure(p.MemoryMB, p.CPUCount); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleCreateVM(conn net.Conn, req Request) {
	var p createVMParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	// Extract VM name from bundlePath if name is empty
	name := p.Name
	if name == "" && p.BundlePath != "" {
		name = filepath.Base(p.BundlePath)
	}
	if err := h.backend.CreateVM(name); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleStartVM(conn net.Conn, req Request) {
	var p startVMParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	// Extract VM name from bundlePath if name is empty
	name := p.Name
	if name == "" && p.BundlePath != "" {
		name = filepath.Base(p.BundlePath)
	}
	if err := h.backend.StartVM(name); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleStopVM(conn net.Conn, req Request) {
	// Desktop sends stopVM with no params at all, so req.Params can be
	// nil. json.Unmarshal(nil, ...) would otherwise reject it as
	// "unexpected end of JSON input" and we'd never reach the backend.
	var p vmNameParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
			return
		}
	}
	if err := h.backend.StopVM(p.Name); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleIsRunning(conn net.Conn, req Request) {
	var p vmNameParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			logx.Debug("isRunning: ignoring malformed params: %v", err)
		}
	}
	running, err := h.backend.IsRunning(p.Name)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, map[string]bool{"running": running})
}

func (h *Handler) handleIsGuestConnected(conn net.Conn, req Request) {
	var p vmNameParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			logx.Debug("isGuestConnected: ignoring malformed params: %v", err)
		}
	}
	connected, err := h.backend.IsGuestConnected(p.Name)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, map[string]bool{"connected": connected})
}

func (h *Handler) handleSpawn(conn net.Conn, req Request) {
	logx.Debug("spawn raw params: %s", logx.Trunc(string(req.Params)))
	var p spawnParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	logx.Debug("spawn parsed: name=%q cmd=%q args=%v cwd=%q env=%v", p.Name, p.Cmd, p.Args, p.Cwd, p.Env)
	processID, err := h.backend.Spawn(p.Name, p.ID, p.Cmd, p.Args, p.Env, p.Cwd, p.AdditionalMounts, req.Params)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, map[string]string{"id": processID})
}

func (h *Handler) handleKill(conn net.Conn, req Request) {
	var p killParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}

	// Delay kill by 1s to let pending result events propagate to the renderer.
	// The Electron app sends kill immediately after receiving the result event,
	// before the UI has time to render the response. This is especially visible
	// in Dispatch where the result never appears in the UI.
	time.Sleep(1 * time.Second)

	if err := h.backend.Kill(p.ProcessID, p.Signal); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleWriteStdin(conn net.Conn, req Request) {
	var p writeStdinParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	logx.Debug("writeStdin processId=%s data=%s", p.ProcessID, logx.Trunc(p.Data))
	if err := h.backend.WriteStdin(p.ProcessID, []byte(p.Data)); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleIsProcessRunning(conn net.Conn, req Request) {
	var p processIDParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	running, err := h.backend.IsProcessRunning(p.ProcessID)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, map[string]bool{"running": running})
}

func (h *Handler) handleMountPath(conn net.Conn, req Request) {
	var p mountPathParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if err := h.backend.MountPath(p.ProcessID, p.Subpath, p.MountName, p.Mode); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleReadFile(conn net.Conn, req Request) {
	var p readFileParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	data, err := h.backend.ReadFile(p.ProcessName, p.FilePath)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	// Desktop's Linux client reads `response.result.content`.
	WriteResponse(conn, req.ID, map[string]interface{}{"content": string(data)})
}

func (h *Handler) handleInstallSdk(conn net.Conn, req Request) {
	var p installSdkParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if err := h.backend.InstallSdk(p.SdkSubpath, p.Version); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleAddApprovedOauthToken(conn net.Conn, req Request) {
	var p oauthTokenParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if err := h.backend.AddApprovedOauthToken(p.Name, p.Token); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleSetDebugLogging(conn net.Conn, req Request) {
	var p debugLoggingParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	h.backend.SetDebugLogging(p.Enabled)
	h.debug = p.Enabled
	logx.SetDebug(p.Enabled)
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleIsDebugLoggingEnabled(conn net.Conn, req Request) {
	WriteResponse(conn, req.ID, map[string]bool{"enabled": h.debug})
}

func (h *Handler) handleSubscribeEvents(conn net.Conn, req Request) {
	var p vmNameParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			logx.Debug("subscribeEvents: ignoring malformed params: %v", err)
		}
	}

	var (
		cancelled int32      // atomic flag to stop callbacks after write failure
		writeMu   sync.Mutex // serialize concurrent event writes on this connection
	)

	cancel, err := h.backend.SubscribeEvents(p.Name, func(event interface{}) {
		if atomic.LoadInt32(&cancelled) != 0 {
			return
		}
		data, err := json.Marshal(event)
		if err != nil {
			logx.Debug("Failed to marshal event: %v", err)
			return
		}
		logx.Debug("EVENT → client: %s", logx.Trunc(string(data)))
		writeMu.Lock()
		werr := WriteMessage(conn, data)
		writeMu.Unlock()
		if werr != nil {
			atomic.StoreInt32(&cancelled, 1)
			logx.Debug("Event write failed, cancelling subscription: %v", werr)
		}
	})
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}

	// Send initial ack
	WriteResponse(conn, req.ID, map[string]bool{"subscribed": true})

	// Block until connection closes (events are pushed via callback)
	// When connection drops, ReadMessage will fail and we cancel
	for {
		if _, err := ReadMessage(conn); err != nil {
			cancel()
			return
		}
	}
}

func (h *Handler) handleGetDownloadStatus(conn net.Conn, req Request) {
	status := h.backend.GetDownloadStatus()
	WriteResponse(conn, req.ID, map[string]string{"status": status})
}

func (h *Handler) handleGetSessionsDiskInfo(conn net.Conn, req Request) {
	var p getSessionsDiskInfoParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	info, err := h.backend.GetSessionsDiskInfo(p.LowWaterBytes)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, info)
}

func (h *Handler) handleDeleteSessionDirs(conn net.Conn, req Request) {
	var p deleteSessionDirsParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	result, err := h.backend.DeleteSessionDirs(p.Names)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, result)
}

func (h *Handler) handleCreateDiskImage(conn net.Conn, req Request) {
	var p createDiskImageParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if err := h.backend.CreateDiskImage(p.DiskName, p.SizeGiB); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleSendGuestResponse(conn net.Conn, req Request) {
	var p sendGuestResponseParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if err := h.backend.SendGuestResponse(p.ID, p.ResultJSON, p.Error); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}
