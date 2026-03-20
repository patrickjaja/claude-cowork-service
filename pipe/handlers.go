package pipe

import (
	"encoding/json"
	"log"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
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
		if h.debug {
			log.Printf("Invalid JSON: %v", err)
		}
		WriteError(conn, nil, -32700, "Parse error")
		return
	}

	if h.debug {
		log.Printf("RPC: %s (id=%v)", req.Method, req.ID)
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
	default:
		if h.debug {
			log.Printf("RPC: unknown method %q — returning success (passthrough)", req.Method)
		}
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
	Name             string                       `json:"name"`
	ID               string                       `json:"id"`
	Cmd              string                       `json:"command"`
	Args             []string                     `json:"args"`
	Env              map[string]string             `json:"env"`
	Cwd              string                       `json:"cwd"`
	AdditionalMounts map[string]additionalMount    `json:"additionalMounts"`
}

type additionalMount struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

type processIDParams struct {
	ProcessID string `json:"id"`
}

type writeStdinParams struct {
	ProcessID string `json:"id"`
	Data      string `json:"data"`
}

type mountPathParams struct {
	Name      string `json:"name"`
	HostPath  string `json:"hostPath"`
	GuestPath string `json:"guestPath"`
}

type readFileParams struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type oauthTokenParams struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

type debugLoggingParams struct {
	Enabled bool `json:"enabled"`
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
	var p vmNameParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
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
		json.Unmarshal(req.Params, &p)
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
		json.Unmarshal(req.Params, &p)
	}
	connected, err := h.backend.IsGuestConnected(p.Name)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, map[string]bool{"connected": connected})
}

func (h *Handler) handleSpawn(conn net.Conn, req Request) {
	if h.debug {
		log.Printf("spawn raw params: %s", string(req.Params))
	}
	var p spawnParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if h.debug {
		log.Printf("spawn parsed: name=%q cmd=%q args=%v cwd=%q env=%v", p.Name, p.Cmd, p.Args, p.Cwd, p.Env)
	}
	// Convert additionalMounts to map[string]string for the backend
	mounts := make(map[string]string, len(p.AdditionalMounts))
	for mountName, mount := range p.AdditionalMounts {
		mounts[mountName] = mount.Path
	}
	processID, err := h.backend.Spawn(p.Name, p.ID, p.Cmd, p.Args, p.Env, p.Cwd, mounts)
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
	if err := h.backend.Kill(p.ProcessID, p.Signal); err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleWriteStdin(conn net.Conn, req Request) {
	if h.debug {
		log.Printf("writeStdin raw params: %s", string(req.Params))
	}
	var p writeStdinParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if h.debug {
		log.Printf("writeStdin processId=%s data=%q", p.ProcessID, p.Data)
		// Log full stdin data to trace skill invocations
		if len(p.Data) > 5000 {
			log.Printf("writeStdin FULL (truncated): %s...END", p.Data[:5000])
		} else {
			log.Printf("writeStdin FULL: %s", p.Data)
		}
	}
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
	if err := h.backend.MountPath(p.Name, p.HostPath, p.GuestPath); err != nil {
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
	data, err := h.backend.ReadFile(p.Name, p.Path)
	if err != nil {
		WriteError(conn, req.ID, -32000, err.Error())
		return
	}
	WriteResponse(conn, req.ID, map[string]interface{}{"data": string(data)})
}

func (h *Handler) handleInstallSdk(conn net.Conn, req Request) {
	var p vmNameParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		WriteError(conn, req.ID, -32602, "Invalid params: "+err.Error())
		return
	}
	if err := h.backend.InstallSdk(p.Name); err != nil {
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
	WriteResponse(conn, req.ID, nil)
}

func (h *Handler) handleIsDebugLoggingEnabled(conn net.Conn, req Request) {
	WriteResponse(conn, req.ID, map[string]bool{"enabled": h.debug})
}

func (h *Handler) handleSubscribeEvents(conn net.Conn, req Request) {
	var p vmNameParams
	if req.Params != nil {
		json.Unmarshal(req.Params, &p)
	}

	var (
		cancelled int32     // atomic flag to stop callbacks after write failure
		writeMu   sync.Mutex // serialize concurrent event writes on this connection
	)

	cancel, err := h.backend.SubscribeEvents(p.Name, func(event interface{}) {
		if atomic.LoadInt32(&cancelled) != 0 {
			return
		}
		data, err := json.Marshal(event)
		if err != nil {
			if h.debug {
				log.Printf("Failed to marshal event: %v", err)
			}
			return
		}
		if h.debug {
			truncated := string(data)
			if len(truncated) > 200 {
				truncated = truncated[:200] + "..."
			}
			log.Printf("EVENT → client: %s", truncated)
		}
		writeMu.Lock()
		werr := WriteMessage(conn, data)
		writeMu.Unlock()
		if werr != nil {
			atomic.StoreInt32(&cancelled, 1)
			if h.debug {
				log.Printf("Event write failed, cancelling subscription: %v", werr)
			}
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
