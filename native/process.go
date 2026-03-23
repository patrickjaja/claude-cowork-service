package native

import (
	"bufio"
	"bytes"
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

	"github.com/patrickjaja/claude-cowork-service/process"
)

// pathRemap represents a from→to byte replacement for path remapping.
type pathRemap struct {
	from []byte
	to   []byte
}

// localProcess tracks a single spawned host process.
type localProcess struct {
	id         string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	done       chan struct{}
	mu         sync.Mutex
	vmPrefix   []byte // e.g. "/sessions/optimistic-nice-brahmagupta"
	realPrefix []byte // e.g. "/home/user/.local/share/claude-cowork/sessions/optimistic-nice-brahmagupta"
	reverseMap bool   // only reverse-map output if VM path exists on filesystem
	mountRemap []pathRemap // remap session/mnt/<mount> paths to real mount targets
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
func (pt *processTracker) spawn(id string, cmd string, args []string, env map[string]string, cwd string, vmPrefix string, realPrefix string, mountRemap []pathRemap) (string, error) {
	if id == "" {
		pt.mu.Lock()
		pt.nextID++
		id = fmt.Sprintf("proc-%d", pt.nextID)
		pt.mu.Unlock()
	}

	// If the given path doesn't exist, try to find it in PATH.
	// Systemd services have minimal PATH (/usr/local/bin:/usr/bin), so we use
	// a multi-stage fallback to locate binaries installed in user-specific locations
	// (npm global, ~/.local/bin, nvm, etc.).
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

		// Stage 4: hardcoded common locations
		if resolved == "" {
			home := os.Getenv("HOME")
			for _, candidate := range []string{
				filepath.Join(home, ".local", "bin", base),
				filepath.Join(home, ".npm-global", "bin", base),
				"/usr/local/bin/" + base,
				"/usr/bin/" + base,
			} {
				if _, statErr := os.Stat(candidate); statErr == nil {
					resolved = candidate
					break
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
		id:         id,
		cmd:        c,
		stdin:      stdin,
		done:       make(chan struct{}),
		mountRemap: mountRemap,
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

		// Remap real paths back to VM paths in output (only if VM path exists)
		if lp != nil && lp.reverseMap {
			line = string(bytes.ReplaceAll([]byte(line), lp.realPrefix, lp.vmPrefix))
		}

		// Detect MCP control_request messages from the CLI.
		// These are JSON lines with "type":"control_request" that the CLI sends
		// when it needs to call an SDK MCP server tool. Log them prominently
		// so we can observe the MCP proxy flow during the experiment.
		if strings.Contains(line, `"type":"control_request"`) || strings.Contains(line, `"type": "control_request"`) {
			log.Printf("[native] >>>MCP-PROXY>>> %s %s control_request detected: %s", id, stream, truncateLine(line, 500))
		}

		if pt.debug {
			truncated := truncateLine(line, 2000)
			// Highlight skill-related messages
			if strings.Contains(strings.ToLower(line), "skill") || strings.Contains(line, "Unknown") {
				log.Printf("[native] !!SKILL!! %s %s: %s", id, stream, truncated)
			} else {
				log.Printf("[native] %s %s: %s", id, stream, truncated)
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

// truncateLine truncates a string to maxLen characters for logging.
func truncateLine(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "...[TRUNCATED]"
	}
	return s
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
		syscall.Kill(-pgid, sig)
	} else {
		lp.cmd.Process.Signal(sig)
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
		log.Printf("[native] <<<MCP-PROXY<<< %s control_response detected: %s", processID, truncateLine(string(data), 500))
	}

	// Also detect initialize messages that may contain sdkMcpServers
	if bytes.Contains(data, []byte(`sdkMcpServers`)) {
		log.Printf("[native] <<<MCP-INIT<<< %s sdkMcpServers in writeStdin: %s", processID, truncateLine(string(data), 1000))
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
		pt.kill(id, "")
	}
}
