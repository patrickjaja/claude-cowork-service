package vm

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/patrickjaja/claude-cowork-service/logx"
)

const (
	afVsock      = 40 // AF_VSOCK
	vmaddrCIDAny = 0xFFFFFFFF
)

// sockaddrVM mirrors Linux struct sockaddr_vm for raw syscalls.
type sockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Flags     uint8
	Zero      [3]uint8
}

// vsockConn wraps a raw AF_VSOCK file descriptor. Go's net package doesn't
// recognize AF_VSOCK (getsockname fails with ENOPROTOOPT), so we bypass it
// entirely and drive the fd through *os.File, which handles EINTR / partial
// IO and lets Close() unblock a blocked Read from another goroutine.
type vsockConn struct {
	file    *os.File
	writeMu sync.Mutex
}

func (c *vsockConn) Read(b []byte) (int, error) { return c.file.Read(b) }
func (c *vsockConn) Close() error               { return c.file.Close() }
func (c *vsockConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// Loop on partial writes — *os.File.Write already loops, but we keep
	// the mutex scope tight so concurrent writers serialize cleanly.
	return c.file.Write(b)
}

// GuestBridge owns the vsock listener and the single accepted guest
// connection. Once the guest sdk-daemon connects it becomes a bidirectional
// length-prefixed JSON channel: requests we send get matched against replies
// by id; unsolicited messages are forwarded as events to the event callback.
type GuestBridge struct {
	port  uint32
	debug bool
	emit  func(event interface{})

	listenFD int

	connMu sync.RWMutex
	conn   *vsockConn

	connected atomic.Bool
	closed    atomic.Bool

	pendMu  sync.Mutex
	pending map[string]chan json.RawMessage
	nextID  uint64

	onConnect func() // called once when guest connects
}

// NewGuestBridge creates a bridge listening on the given vsock port.
func NewGuestBridge(port uint32, debug bool, emit func(event interface{})) *GuestBridge {
	return &GuestBridge{
		port:     port,
		debug:    debug,
		emit:     emit,
		listenFD: -1,
		pending:  make(map[string]chan json.RawMessage),
	}
}

// Listen opens AF_VSOCK and starts accepting the guest connection.
// onConnect fires when the first guest connection is accepted.
func (g *GuestBridge) Listen(onConnect func()) error {
	g.onConnect = onConnect

	fd, err := syscall.Socket(afVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("creating vsock socket: %w", err)
	}

	addr := sockaddrVM{Family: afVsock, Port: g.port, CID: vmaddrCIDAny}
	addrPtr := unsafe.Pointer(&addr)
	if _, _, errno := syscall.RawSyscall(
		syscall.SYS_BIND, uintptr(fd), uintptr(addrPtr), unsafe.Sizeof(addr),
	); errno != 0 {
		_ = syscall.Close(fd) // socket is being discarded; close failure can't be acted on
		return fmt.Errorf("binding vsock port %d: %w", g.port, errno)
	}
	if err := syscall.Listen(fd, 1); err != nil {
		_ = syscall.Close(fd)
		return fmt.Errorf("listening vsock: %w", err)
	}
	g.listenFD = fd

	go g.acceptLoop()
	return nil
}

func (g *GuestBridge) acceptLoop() {
	for {
		// Go's syscall.Accept tries to decode the peer address via
		// anyToSockaddr, which rejects AF_VSOCK. Call accept4 directly
		// with NULL addr/addrlen so no sockaddr parsing happens.
		nfdR, _, errno := syscall.Syscall6(
			syscall.SYS_ACCEPT4, uintptr(g.listenFD), 0, 0, 0, 0, 0,
		)
		if errno != 0 {
			if g.closed.Load() {
				return
			}
			if g.debug {
				log.Printf("[kvm] vsock accept: %v", errno)
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		nfd := int(nfdR)

		conn := &vsockConn{file: os.NewFile(uintptr(nfd), "vsock-guest")}

		g.connMu.Lock()
		if g.conn != nil {
			if err := g.conn.Close(); err != nil && g.debug {
				log.Printf("[kvm] close prior guest conn: %v", err)
			}
		}
		g.conn = conn
		g.connMu.Unlock()

		g.connected.Store(true)
		log.Printf("[kvm] sdk-daemon connected via vsock")

		onConnect := g.onConnect
		g.onConnect = nil // fire only once
		if onConnect != nil {
			go onConnect()
		}

		g.readLoop(conn)

		g.connected.Store(false)
		g.connMu.Lock()
		if g.conn == conn {
			g.conn = nil
		}
		g.connMu.Unlock()
		if err := conn.Close(); err != nil && g.debug {
			log.Printf("[kvm] close guest conn: %v", err)
		}
		log.Printf("[kvm] guest connection closed")
		g.emit(map[string]string{"type": "networkStatus", "status": "disconnected"})

		g.pendMu.Lock()
		for id, ch := range g.pending {
			close(ch)
			delete(g.pending, id)
		}
		g.pendMu.Unlock()
	}
}

// IsConnected reports whether the guest is currently connected.
func (g *GuestBridge) IsConnected() bool {
	return g.connected.Load()
}

// Close tears down the listener and any active connection.
func (g *GuestBridge) Close() {
	g.closed.Store(true)
	g.connMu.Lock()
	if g.conn != nil {
		if err := g.conn.Close(); err != nil && g.debug {
			log.Printf("[kvm] Close guest conn: %v", err)
		}
		g.conn = nil
	}
	g.connMu.Unlock()
	if g.listenFD >= 0 {
		if err := syscall.Close(g.listenFD); err != nil && g.debug {
			log.Printf("[kvm] Close listen fd: %v", err)
		}
		g.listenFD = -1
	}
}

// forwardedEvents are guest→host messages that get re-emitted as events to
// our subscribers. Everything else is logged + dropped.
var forwardedEvents = map[string]bool{
	"stdout": true, "stderr": true, "exit": true,
	"networkStatus": true, "apiReachability": true,
	"ready": true, "startupStep": true,
}

func (g *GuestBridge) readLoop(conn *vsockConn) {
	for {
		msg, err := readFramed(conn)
		if err != nil {
			if err != io.EOF && g.debug {
				log.Printf("[kvm] guest read: %v", err)
			}
			return
		}
		g.handleMessage(msg)
	}
}

func (g *GuestBridge) handleMessage(raw []byte) {
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		if g.debug {
			log.Printf("[kvm] guest JSON parse: %v", err)
		}
		return
	}

	if g.debug {
		log.Printf("[kvm] guest message: %s", logx.Trunc(string(raw)))
	}

	typ := jsonString(msg["type"])

	if forwardedEvents[typ] {
		var out interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			if g.debug {
				log.Printf("[kvm] forward %s: re-unmarshal: %v", typ, err)
			}
			return
		}
		g.emit(out)
		return
	}

	// Nested event form: {"type":"event","event":"networkStatus","params":{...}}
	if typ == "event" {
		ev := jsonString(msg["event"])
		if forwardedEvents[ev] {
			var params map[string]interface{}
			if p, ok := msg["params"]; ok {
				if err := json.Unmarshal(p, &params); err != nil && g.debug {
					log.Printf("[kvm] nested event %s: params unmarshal: %v", ev, err)
				}
			}
			if params == nil {
				params = map[string]interface{}{}
			}
			params["type"] = ev
			g.emit(params)
			return
		}
	}

	// Response to a pending request.
	if typ == "response" || msg["success"] != nil || msg["result"] != nil || msg["error"] != nil {
		if idRaw, ok := msg["id"]; ok {
			id := normalizeID(idRaw)
			g.pendMu.Lock()
			ch, found := g.pending[id]
			if found {
				delete(g.pending, id)
			}
			g.pendMu.Unlock()
			if found {
				if result, ok := msg["result"]; ok {
					ch <- result
				} else {
					ch <- raw
				}
				close(ch)
				return
			}
		}
	}

	if g.debug {
		log.Printf("[kvm] unhandled guest message: %s", logx.Trunc(string(raw)))
	}
}

// Forward sends a request to the guest and waits up to 30s for a reply.
// The reply's "result" payload (or whole response if absent) is returned raw.
func (g *GuestBridge) Forward(method string, params interface{}) (json.RawMessage, error) {
	g.connMu.RLock()
	conn := g.conn
	g.connMu.RUnlock()
	if conn == nil || !g.connected.Load() {
		return nil, fmt.Errorf("guest not connected")
	}

	id := strconv.FormatUint(atomic.AddUint64(&g.nextID, 1), 10)
	ch := make(chan json.RawMessage, 1)
	g.pendMu.Lock()
	g.pending[id] = ch
	g.pendMu.Unlock()

	req := map[string]interface{}{
		"type":   "request",
		"method": method,
		"params": params,
		"id":     id,
	}
	if err := g.write(conn, req); err != nil {
		g.pendMu.Lock()
		delete(g.pending, id)
		g.pendMu.Unlock()
		return nil, fmt.Errorf("writing to guest: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("guest disconnected while waiting for %s", method)
		}
		return resp, nil
	case <-time.After(30 * time.Second):
		g.pendMu.Lock()
		delete(g.pending, id)
		g.pendMu.Unlock()
		return nil, fmt.Errorf("guest timeout waiting for %s", method)
	}
}

// Notify sends a fire-and-forget notification to the guest (no response).
func (g *GuestBridge) Notify(method string, params interface{}) error {
	g.connMu.RLock()
	conn := g.conn
	g.connMu.RUnlock()
	if conn == nil || !g.connected.Load() {
		return fmt.Errorf("guest not connected")
	}
	return g.write(conn, map[string]interface{}{
		"type":   "notification",
		"method": method,
		"params": params,
	})
}

func (g *GuestBridge) write(conn *vsockConn, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(data)))
	copy(buf[4:], data)
	_, err = conn.Write(buf)
	return err
}

// readFramed reads a length-prefixed JSON message. Returns io.EOF on clean close.
func readFramed(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length > 10*1024*1024 {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// normalizeID stringifies a JSON id value (string or number) so that matching
// works regardless of which form the guest uses.
func normalizeID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return string(raw)
}
