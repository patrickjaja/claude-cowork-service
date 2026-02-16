package native

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/patrickjaja/claude-cowork-service/process"
)

// localProcess tracks a single spawned host process.
type localProcess struct {
	id         string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	done       chan struct{}
	mu         sync.Mutex
	vmPrefix   []byte // e.g. "/sessions/optimistic-nice-brahmagupta"
	realPrefix []byte // e.g. "/home/user/.local/share/claude-cowork/sessions/optimistic-nice-brahmagupta"
}

// processTracker manages all spawned processes and streams their output via event callbacks.
type processTracker struct {
	processes map[string]*localProcess
	nextID    int
	emit      func(event interface{})
	debug     bool
	mu        sync.RWMutex
}

func newProcessTracker(emit func(event interface{}), debug bool) *processTracker {
	return &processTracker{
		processes: make(map[string]*localProcess),
		emit:      emit,
		debug:     debug,
	}
}

// spawn starts a new process and streams its stdout/stderr via events.
func (pt *processTracker) spawn(id string, cmd string, args []string, env map[string]string, cwd string, vmPrefix string, realPrefix string) (string, error) {
	if id == "" {
		pt.mu.Lock()
		pt.nextID++
		id = fmt.Sprintf("proc-%d", pt.nextID)
		pt.mu.Unlock()
	}

	// If the given path doesn't exist, try to find it in PATH
	if _, err := os.Stat(cmd); err != nil {
		if resolved, lookErr := exec.LookPath(filepath.Base(cmd)); lookErr == nil {
			if pt.debug {
				log.Printf("[native] resolved %s → %s", cmd, resolved)
			}
			cmd = resolved
		} else {
			// Fallback: check common Linux install locations
			// (systemd user services have minimal PATH, missing ~/.local/bin)
			base := filepath.Base(cmd)
			home := os.Getenv("HOME")
			candidates := []string{
				filepath.Join(home, ".local", "bin", base),
				"/usr/local/bin/" + base,
				"/usr/bin/" + base,
			}
			for _, candidate := range candidates {
				if _, statErr := os.Stat(candidate); statErr == nil {
					if pt.debug {
						log.Printf("[native] fallback resolved %s → %s", cmd, candidate)
					}
					cmd = candidate
					break
				}
			}
		}
	}

	c := exec.Command(cmd, args...)
	if cwd != "" {
		c.Dir = cwd
	}
	if len(env) > 0 {
		// Start with current environment and overlay requested vars
		c.Env = c.Environ()
		for k, v := range env {
			c.Env = append(c.Env, k+"="+v)
		}
	}

	// Set up process group so we can kill children too
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := c.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdout, err := c.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderr, err := c.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := c.Start(); err != nil {
		return "", fmt.Errorf("starting process: %w", err)
	}

	lp := &localProcess{
		id:    id,
		cmd:   c,
		stdin: stdin,
		done:  make(chan struct{}),
	}
	if vmPrefix != "" && realPrefix != "" {
		lp.vmPrefix = []byte(vmPrefix)
		lp.realPrefix = []byte(realPrefix)
	}

	pt.mu.Lock()
	pt.processes[id] = lp
	pt.mu.Unlock()

	if pt.debug {
		log.Printf("[native] spawned %s: %s %v (pid=%d)", id, cmd, args, c.Process.Pid)
	}

	// Stream stdout/stderr in goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		pt.streamOutput(id, stdout, "stdout")
	}()

	go func() {
		defer wg.Done()
		pt.streamOutput(id, stderr, "stderr")
	}()

	// Wait for process exit in background
	go func() {
		wg.Wait() // wait for output streams to drain first
		err := c.Wait()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				code = -1
			}
		}

		if pt.debug {
			log.Printf("[native] %s exited with code %d", id, code)
		}

		pt.emit(process.NewExitEvent(id, code))
		close(lp.done)
	}()

	return id, nil
}

// streamOutput reads lines from a reader and emits events.
// Claude Code sends its stream-json output on stderr, so we emit both
// stdout and stderr data as "stdout" events — that's what the client reads.
func (pt *processTracker) streamOutput(id string, r io.Reader, stream string) {
	// Look up process for reverse path mapping
	pt.mu.RLock()
	lp := pt.processes[id]
	pt.mu.RUnlock()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 10MB max for large Opus stream-json lines
	for scanner.Scan() {
		line := scanner.Text() + "\n"

		// Remap real paths back to VM paths in output
		if lp != nil && lp.realPrefix != nil {
			line = string(bytes.ReplaceAll([]byte(line), lp.realPrefix, lp.vmPrefix))
		}
		if pt.debug {
			truncated := line
			if len(truncated) > 200 {
				truncated = truncated[:200] + "..."
			}
			log.Printf("[native] %s %s: %s", id, stream, truncated)
		}

		// Intercept mcp_message control_requests from Claude Code and
		// auto-respond with an error so it doesn't block waiting for us
		// to proxy MCP connections.
		if handled := pt.handleMcpRequest(id, line); handled {
			continue // don't forward to client
		}

		// Always emit as stdout — Claude Desktop only processes stdout events,
		// and Claude Code writes its stream-json data to stderr.
		pt.emit(process.NewStdoutEvent(id, line))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[native] %s %s scanner error: %v", id, stream, err)
	}
}

// handleMcpRequest checks if a line is an mcp_message control_request from
// Claude Code and auto-responds with an error on stdin so Claude Code
// doesn't block. Returns true if the message was handled (intercepted).
func (pt *processTracker) handleMcpRequest(id string, line string) bool {
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return false
	}
	if msg["type"] != "control_request" {
		return false
	}
	req, ok := msg["request"].(map[string]interface{})
	if !ok || req["subtype"] != "mcp_message" {
		return false
	}
	requestID, _ := msg["request_id"].(string)
	serverName, _ := req["server_name"].(string)
	if requestID == "" {
		return false
	}

	if pt.debug {
		log.Printf("[native] intercepted mcp_message for %q, sending error response", serverName)
	}

	// Send a control_response error back to Claude Code's stdin
	resp := map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "error",
			"request_id": requestID,
			"error":      fmt.Sprintf("MCP server %q not available in native mode", serverName),
		},
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return false
	}
	respBytes = append(respBytes, '\n')

	if err := pt.writeStdin(id, respBytes); err != nil {
		if pt.debug {
			log.Printf("[native] failed to send mcp error response: %v", err)
		}
	}
	return true
}

// kill sends SIGTERM to a process, falling back to SIGKILL.
func (pt *processTracker) kill(processID string) error {
	pt.mu.RLock()
	lp, ok := pt.processes[processID]
	pt.mu.RUnlock()

	if !ok {
		return fmt.Errorf("process %s not found", processID)
	}

	if lp.cmd.Process == nil {
		return nil
	}

	// Kill the entire process group
	pgid, err := syscall.Getpgid(lp.cmd.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		lp.cmd.Process.Signal(syscall.SIGTERM)
	}

	return nil
}

// writeStdin writes data to a process's stdin pipe with timeout and exit checks.
func (pt *processTracker) writeStdin(processID string, data []byte) error {
	pt.mu.RLock()
	lp, ok := pt.processes[processID]
	pt.mu.RUnlock()

	if !ok {
		return fmt.Errorf("process %s not found", processID)
	}

	// Remap VM paths to real paths in stdin data
	if lp.vmPrefix != nil {
		data = bytes.ReplaceAll(data, lp.vmPrefix, lp.realPrefix)
	}

	// Check if process already exited
	select {
	case <-lp.done:
		return fmt.Errorf("process %s has exited", processID)
	default:
	}

	// Write with timeout to avoid blocking forever if stdin buffer is full
	type writeResult struct{ err error }
	ch := make(chan writeResult, 1)
	go func() {
		lp.mu.Lock()
		defer lp.mu.Unlock()
		_, err := lp.stdin.Write(data)
		ch <- writeResult{err}
	}()

	select {
	case res := <-ch:
		return res.err
	case <-lp.done:
		return fmt.Errorf("process %s exited during write", processID)
	case <-time.After(10 * time.Second):
		return fmt.Errorf("stdin write timeout for process %s", processID)
	}
}

// isRunning checks if a tracked process is still running.
func (pt *processTracker) isRunning(processID string) (bool, error) {
	pt.mu.RLock()
	lp, ok := pt.processes[processID]
	pt.mu.RUnlock()

	if !ok {
		return false, nil
	}

	select {
	case <-lp.done:
		return false, nil
	default:
		return true, nil
	}
}

// killAll terminates all tracked processes.
func (pt *processTracker) killAll() {
	pt.mu.RLock()
	ids := make([]string, 0, len(pt.processes))
	for id := range pt.processes {
		ids = append(ids, id)
	}
	pt.mu.RUnlock()

	for _, id := range ids {
		pt.kill(id)
	}
}
