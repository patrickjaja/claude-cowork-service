package pipe

import (
	"log"
	"net"
	"os"
	"sync"
)

// VMBackend defines the interface that the VM manager must implement.
// This decouples the pipe server from the VM implementation.
type VMBackend interface {
	Configure(memoryMB int, cpuCount int) error
	CreateVM(name string) error
	StartVM(name string) error
	StopVM(name string) error
	IsRunning(name string) (bool, error)
	IsGuestConnected(name string) (bool, error)
	Spawn(name string, id string, cmd string, args []string, env map[string]string, cwd string, mounts map[string]string) (string, error)
	Kill(processID string, signal string) error
	WriteStdin(processID string, data []byte) error
	IsProcessRunning(processID string) (bool, error)
	MountPath(processID string, subpath string, mountName string, mode string) error
	ReadFile(processName string, filePath string) ([]byte, error)
	InstallSdk(name string) error
	AddApprovedOauthToken(name string, token string) error
	SetDebugLogging(enabled bool)
	SubscribeEvents(name string, callback func(event interface{})) (cancel func(), err error)
	GetDownloadStatus() string
	SendGuestResponse(id string, resultJSON string, errMsg string) error
}

// Server manages the Unix domain socket and client connections.
type Server struct {
	socketPath string
	backend    VMBackend
	debug      bool
	listener   net.Listener
	wg         sync.WaitGroup
	quit       chan struct{}
}

// NewServer creates a new Unix socket server.
func NewServer(socketPath string, backend VMBackend, debug bool) *Server {
	return &Server{
		socketPath: socketPath,
		backend:    backend,
		debug:      debug,
		quit:       make(chan struct{}),
	}
}

// Start begins listening on the Unix socket.
func (s *Server) Start() error {
	// Remove stale socket file if it exists
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = listener

	// Set socket permissions (readable/writable by owner only)
	if err := os.Chmod(s.socketPath, 0700); err != nil {
		listener.Close()
		return err
	}

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	close(s.quit)
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	os.Remove(s.socketPath)
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	if s.debug {
		log.Printf("Client connected: %s", conn.RemoteAddr())
	}

	handler := NewHandler(s.backend, s.debug)

	for {
		select {
		case <-s.quit:
			return
		default:
		}

		payload, err := ReadMessage(conn)
		if err != nil {
			if s.debug {
				log.Printf("Client disconnected: %v", err)
			}
			return
		}

		handler.Handle(conn, payload)
	}
}
