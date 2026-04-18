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
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/patrickjaja/claude-cowork-service/logx"
	"github.com/patrickjaja/claude-cowork-service/process"
)

// pathRemap represents a from→to byte replacement for path remapping.
type pathRemap struct {
	from []byte
	to   []byte
}

// localProcess tracks a single spawned host process.
type localProcess struct {
	id                string
	cmd               *exec.Cmd
	stdin             io.WriteCloser
	done              chan struct{}
	mu                sync.Mutex
	vmPrefix          []byte      // e.g. "/sessions/optimistic-nice-brahmagupta"
	realPrefix        []byte      // e.g. "/home/user/.local/share/claude-cowork/sessions/optimistic-nice-brahmagupta"
	reverseMap        bool        // only reverse-map output if VM path exists on filesystem
	mountRemap        []pathRemap // fwd: session/mnt/<mount> → real host path (for stdin)
	reverseMountRemap []pathRemap // rev: real host path → VM /sessions/<name>/mnt/<mount> (for stdout)
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
func (pt *processTracker) spawn(id string, cmd string, args []string, env map[string]string, cwd string, vmPrefix string, realPrefix string, mountRemap []pathRemap, reverseMountRemap []pathRemap) (string, error) {
	if id == "" {
		pt.mu.Lock()
		pt.nextID++
		id = fmt.Sprintf("proc-%d", pt.nextID)
		pt.mu.Unlock()
	}

	// If the given path doesn't exist, try to find it in PATH.
	// Systemd services have minimal PATH (/usr/local/bin:/usr/bin), so we use
	// multi-stage shell-based fallbacks to locate binaries installed in
	// user-specific locations (npm global, ~/.local/bin, nvm, etc.).
	if _, err := os.Stat(cmd); err != nil {
		base := filepath.Base(cmd)
		resolved := ""

		// Stage 1: exec.LookPath — checks current process PATH
		if r, lookErr := exec.LookPath(base); lookErr == nil {
			resolved = r
		}

		// Stage 2: login shell — bash -lc loads ~/.bash_profile / ~/.profile
		if resolved == "" {
			if out, err := exec.Command("bash", "-lc", "which "+base).Output(); err == nil {
				r := filepath.Clean(string(bytes.TrimSpace(out)))
				if _, err := os.Stat(r); err == nil {
					resolved = r
				}
			}
		}

		// Stage 3: interactive login shell — loads ~/.bashrc too (PATH additions
		// are often in .bashrc behind an interactive guard: [[ $- != *i* ]] && return).
		// Output may include shell init noise (fastfetch, motd, etc.), so we parse
		// all lines for an absolute path that exists on disk.
		if resolved == "" {
			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "bash"
			}
			if out, err := exec.Command(shell, "-lic", "command -v "+base).Output(); err == nil {
				for _, line := range strings.Split(string(out), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "/") && !strings.ContainsAny(line, " \t") {
						if _, err := os.Stat(line); err == nil {
							resolved = line
						}
					}
				}
			}
		}

		if resolved != "" {
			if pt.debug {
				log.Printf("[native] resolved %s → %s", cmd, resolved)
			}
			cmd = resolved
		} else if pt.debug {
			log.Printf("[native] WARNING: could not resolve %s in any fallback stage", cmd)
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

	// Strip env vars that prevent nested Claude Code execution.
	// When cowork-svc is started from within a Claude Code session, it inherits
	// CLAUDECODE/CLAUDE_CODE_ENTRYPOINT which cause spawned CLI instances to
	// refuse to start ("cannot be launched inside another Claude Code session").
	if c.Env == nil {
		c.Env = os.Environ()
	}
	for i := len(c.Env) - 1; i >= 0; i-- {
		for _, prefix := range []string{"CLAUDECODE=", "CLAUDE_CODE_ENTRYPOINT="} {
			if strings.HasPrefix(c.Env[i], prefix) {
				c.Env = append(c.Env[:i], c.Env[i+1:]...)
				break
			}
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
		pt.emit(process.NewErrorEvent(id, fmt.Sprintf("failed to start process: %v", err), true))
		return "", fmt.Errorf("starting process: %w", err)
	}

	lp := &localProcess{
		id:                id,
		cmd:               c,
		stdin:             stdin,
		done:              make(chan struct{}),
		mountRemap:        mountRemap,
		reverseMountRemap: reverseMountRemap,
	}
	if vmPrefix != "" && realPrefix != "" {
		lp.vmPrefix = []byte(vmPrefix)
		lp.realPrefix = []byte(realPrefix)
		// Only reverse-map output if the VM path exists on the filesystem.
		// Without root, /sessions/<name> can't be created, so reverse-mapping
		// would produce paths the model can't access for tool calls.
		if _, err := os.Stat(vmPrefix); err == nil {
			lp.reverseMap = true
		} else if pt.debug {
			log.Printf("[native] VM path %s not accessible, disabling output reverse-mapping", vmPrefix)
		}
	}

	pt.mu.Lock()
	pt.processes[id] = lp
	pt.mu.Unlock()

	if pt.debug {
		log.Printf("[native] spawned %s: %s %v (pid=%d)", id, cmd, args, c.Process.Pid)
		log.Printf("[native] === FULL SPAWN ARGS for %s ===", id)
		log.Printf("[native]   cmd: %s", cmd)
		for i, a := range args {
			log.Printf("[native]   arg[%d]: %s", i, a)
		}
		log.Printf("[native]   cwd: %s", cwd)
		log.Printf("[native] === END ARGS ===")
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
		sig := ""
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
				// Detect signal-caused exits
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					sig = signalName(status.Signal())
				}
			} else {
				code = -1
			}
		}

		if pt.debug {
			if sig != "" {
				log.Printf("[native] %s exited with code %d (signal=%s)", id, code, sig)
			} else {
				log.Printf("[native] %s exited with code %d", id, code)
			}
		}

		if sig != "" {
			pt.emit(process.NewExitEventWithSignal(id, code, sig))
		} else {
			pt.emit(process.NewExitEvent(id, code))
		}
		close(lp.done)
	}()

	return id, nil
}

// streamOutput reads lines from a reader and emits events.
// Claude Code sends its stream-json output on stderr, so we emit both
// stdout and stderr data as "stdout" events — that's what the client reads.
//
// When -keep-mcp-config is active, this also detects control_request messages
// from the CLI (used for SDK MCP server communication). These are logged for
// debugging but still emitted as stdout events — Claude Desktop's session
// manager is expected to intercept and handle them (same as in VM mode).
func (pt *processTracker) streamOutput(id string, r io.Reader, stream string) {
	// Look up process for reverse path mapping
	pt.mu.RLock()
	lp := pt.processes[id]
	pt.mu.RUnlock()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 10MB max for large Opus stream-json lines
	for scanner.Scan() {
		line := scanner.Text() + "\n"

		// Remap real mount target paths → VM mount paths in output.
		// Must happen BEFORE the session prefix remap (more specific first).
		// Example: /home/user/.config/Claude/.../outputs/ → /sessions/<name>/mnt/outputs/
		if lp != nil {
			for _, rm := range lp.reverseMountRemap {
				line = string(bytes.ReplaceAll([]byte(line), rm.from, rm.to))
			}
		}

		// Remap real session prefix → VM session prefix in output
		if lp != nil && lp.reverseMap {
			line = string(bytes.ReplaceAll([]byte(line), lp.realPrefix, lp.vmPrefix))
		}

		// Intercept present_files MCP calls and handle locally on native Linux.
		// Desktop's present_files validates paths against VM mounts, which fails
		// for native Linux paths. Since there's no VM boundary, we just verify
		// the files exist and return success directly to the CLI.
		if lp != nil && (strings.Contains(line, `"type":"control_request"`) || strings.Contains(line, `"type": "control_request"`)) {
			if handled := pt.tryHandlePresentFiles(lp, line); handled {
				continue // don't forward to Desktop
			}
		}

		// Detect MCP control_request messages from the CLI.
		if pt.debug && (strings.Contains(line, `"type":"control_request"`) || strings.Contains(line, `"type": "control_request"`)) {
			logx.Debug("[native] >>>MCP-PROXY>>> %s %s control_request detected: %s", id, stream, logx.Trunc(line))
		}

		if pt.debug {
			truncated := logx.Trunc(line)
			// Highlight skill-related messages
			if strings.Contains(strings.ToLower(line), "skill") || strings.Contains(line, "Unknown") {
				logx.Debug("[native] !!SKILL!! %s %s: %s", id, stream, truncated)
			} else {
				logx.Debug("[native] %s %s: %s", id, stream, truncated)
			}
		}

		// Always emit as stdout — Claude Desktop only processes stdout events,
		// and Claude Code writes its stream-json data to stderr.
		pt.emit(process.NewStdoutEvent(id, line))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[native] %s %s scanner error: %v", id, stream, err)
		pt.emit(process.NewErrorEvent(id, fmt.Sprintf("%s scanner error: %v", stream, err), false))
	}
}

// tryHandlePresentFiles intercepts mcp__cowork__present_files control_requests
// and handles them locally on native Linux. Returns true if handled.
//
// On the VM (Windows/Mac), present_files triggers file transfer from VM to host.
// On native Linux, the files are already on the host filesystem — we just need
// to verify they exist and return success. Desktop's present_files handler would
// fail because it validates against VM-style mount paths.
func (pt *processTracker) tryHandlePresentFiles(lp *localProcess, line string) bool {
	// Quick check before parsing JSON
	if !strings.Contains(line, "present_files") {
		return false
	}

	// Parse the control_request.
	// Actual format: {"type":"control_request","request_id":"...","request":{"subtype":"mcp_message","server_name":"cowork","message":{"method":"tools/call","params":{"name":"present_files","arguments":{...}}}}}
	var req struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype    string `json:"subtype"`
			ServerName string `json:"server_name"`
			Message    struct {
				Method string `json:"method"`
				Params struct {
					Name string          `json:"name"`
					Args json.RawMessage `json:"arguments"`
				} `json:"params"`
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
			} `json:"message"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &req); err != nil {
		return false
	}
	if req.Type != "control_request" || req.Request.Subtype != "mcp_message" {
		return false
	}

	// Check if this is a present_files tool call
	toolName := req.Request.Message.Params.Name
	if toolName != "present_files" {
		return false
	}

	requestID := req.RequestID

	// Parse the file arguments — present_files takes {files: [{file_path: "..."}]}
	var args struct {
		Files []struct {
			FilePath string `json:"file_path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(req.Request.Message.Params.Args, &args); err != nil {
		log.Printf("[native] present_files: failed to parse args: %v", err)
		return false
	}

	// Reverse the path mappings so we check real paths on disk.
	// The line has already been reverse-mapped (real→VM), so we need to
	// undo that to get back to real filesystem paths for os.Stat.
	var presented []string
	var missing []string
	for _, f := range args.Files {
		realPath := f.FilePath
		// Undo VM prefix → real prefix
		if lp.vmPrefix != nil && lp.realPrefix != nil {
			realPath = strings.ReplaceAll(realPath, string(lp.vmPrefix), string(lp.realPrefix))
		}
		// Undo reverse mount mapping (VM mount → real host path)
		for _, rm := range lp.mountRemap {
			realPath = strings.ReplaceAll(realPath, string(rm.from), string(rm.to))
		}

		if _, err := os.Stat(realPath); err == nil {
			presented = append(presented, realPath)
		} else {
			missing = append(missing, realPath)
		}
	}

	// Build response.
	// Desktop's renderer treats each {type:"text", text:...} entry in the result
	// as a file path and calls readLocalFile on it to display file cards. We must
	// return individual file paths — NOT descriptive text — to match this contract.
	//
	// After the file paths, we append a hint telling the model to also deliver
	// the files via SendUserMessage's attachments parameter. Desktop logs a
	// harmless warning for this non-path item, but processes the real paths fine.
	// Without this hint, the model often skips attachments and uses markdown
	// links that don't reach remote/mobile dispatch users.
	isError := false
	var contentItems []map[string]interface{}

	if len(missing) > 0 {
		isError = true
		errText := fmt.Sprintf("Cannot present %d file(s) — not found on disk:\n", len(missing))
		for _, p := range missing {
			errText += "  - " + p + "\n"
		}
		contentItems = append(contentItems, map[string]interface{}{
			"type": "text",
			"text": errText,
		})
	} else {
		// Return each file path as a separate content item (matches Desktop's format).
		for _, p := range presented {
			contentItems = append(contentItems, map[string]interface{}{
				"type": "text",
				"text": p,
			})
		}
		// Hint the model to also use SendUserMessage with attachments.
		// present_files creates Desktop UI cards but they don't reach mobile/remote
		// dispatch users. The attachments parameter on SendUserMessage uploads files
		// for delivery to the remote client.
		var paths []string
		paths = append(paths, presented...)
		contentItems = append(contentItems, map[string]interface{}{
			"type": "text",
			"text": fmt.Sprintf("NOTE: present_files cards may not be visible to the user (mobile/remote). To ensure delivery, also call SendUserMessage and include the file paths in the attachments parameter: %v", paths),
		})
	}

	log.Printf("[native] present_files handled locally: %d presented, %d missing", len(presented), len(missing))

	// Build synthetic control_response matching Desktop's format
	response := map[string]interface{}{
		"type": "control_response",
		"response": map[string]interface{}{
			"subtype":    "success",
			"request_id": requestID,
			"response": map[string]interface{}{
				"mcp_response": map[string]interface{}{
					"result": map[string]interface{}{
						"content": contentItems,
						"isError": isError,
					},
					"jsonrpc": req.Request.Message.JSONRPC,
					"id":      req.Request.Message.ID,
				},
			},
		},
	}

	respBytes, err := json.Marshal(response)
	if err != nil {
		log.Printf("[native] present_files: failed to marshal response: %v", err)
		return false
	}
	respBytes = append(respBytes, '\n')

	// Write response directly to CLI's stdin
	lp.mu.Lock()
	_, writeErr := lp.stdin.Write(respBytes)
	lp.mu.Unlock()
	if writeErr != nil {
		log.Printf("[native] present_files: failed to write response: %v", writeErr)
		return false
	}

	return true
}

// kill sends a signal to a process. If signal is empty, defaults to SIGTERM.
func (pt *processTracker) kill(processID string, signal string) error {
	pt.mu.RLock()
	lp, ok := pt.processes[processID]
	pt.mu.RUnlock()

	if !ok {
		return fmt.Errorf("process %s not found", processID)
	}

	if lp.cmd.Process == nil {
		return nil
	}

	sig := mapSignal(signal)

	// Kill the entire process group
	pgid, err := syscall.Getpgid(lp.cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, sig)
	} else {
		_ = lp.cmd.Process.Signal(sig)
	}

	return nil
}

// mapSignal maps a signal name string to a syscall.Signal.
// Defaults to SIGTERM if the signal is empty or unrecognized.
func mapSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimPrefix(strings.ToUpper(name), "SIG")) {
	case "KILL":
		return syscall.SIGKILL
	case "INT":
		return syscall.SIGINT
	case "QUIT":
		return syscall.SIGQUIT
	case "HUP":
		return syscall.SIGHUP
	case "USR1":
		return syscall.SIGUSR1
	case "USR2":
		return syscall.SIGUSR2
	default:
		return syscall.SIGTERM
	}
}

// signalName returns the name of a signal (e.g. "SIGTERM").
func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	default:
		return fmt.Sprintf("SIG%d", int(sig))
	}
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

	// Remap session/mnt/<mount> paths to real mount targets.
	// Glob doesn't follow directory symlinks, so the model must see
	// the real target paths instead of symlinked mnt/ paths.
	for _, rm := range lp.mountRemap {
		data = bytes.ReplaceAll(data, rm.from, rm.to)
	}

	// Detect MCP control_response messages from Claude Desktop.
	// These are JSON objects with "type":"control_response" sent by Desktop's
	// session manager in response to control_request messages from the CLI.
	// Log them prominently to observe the MCP proxy flow during the experiment.
	if bytes.Contains(data, []byte(`"type":"control_response"`)) || bytes.Contains(data, []byte(`"type": "control_response"`)) {
		logx.Debug("[native] <<<MCP-PROXY<<< %s control_response detected: %s", processID, logx.Trunc(string(data)))
	}

	// Also detect initialize messages that may contain sdkMcpServers
	if bytes.Contains(data, []byte(`sdkMcpServers`)) {
		logx.Debug("[native] <<<MCP-INIT<<< %s sdkMcpServers in writeStdin: %s", processID, logx.Trunc(string(data)))
	}

	// Strip plugin prefix from skill invocations in user messages.
	// The Cowork UI sends "/document-skills:pdf ..." but the CLI expects
	// just "/pdf ..." (bare skill name). The plugin prefix in the UI
	// (from marketplace.json) doesn't match the CLI's plugin.json name,
	// so we strip it to let the CLI resolve by userFacingName().
	if bytes.Contains(data, []byte(`"content":"/`)) {
		re := regexp.MustCompile(`"content":"/[a-zA-Z0-9_-]+:`)
		if re.Match(data) {
			if pt.debug {
				log.Printf("[native] stripping skill plugin prefix from user message")
			}
			data = re.ReplaceAll(data, []byte(`"content":"/`))
		}
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
		_ = pt.kill(id, "")
	}
}
