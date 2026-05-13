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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/patrickjaja/claude-cowork-service/logx"
	"github.com/patrickjaja/claude-cowork-service/process"
)

// localProcess tracks a single spawned host process.
type localProcess struct {
	id    string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}
	mu    sync.Mutex
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

func resolveExecutable(cmd string, debug bool) string {
	// If the given path doesn't exist, try to find it in PATH.
	// Systemd services have minimal PATH (/usr/local/bin:/usr/bin), so we use
	// multi-stage shell-based fallbacks to locate binaries installed in
	// user-specific locations (npm global, ~/.local/bin, nvm, etc.).
	if _, err := os.Stat(cmd); err == nil {
		return cmd
	}

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
		if debug {
			log.Printf("[native] resolved %s → %s", cmd, resolved)
		}
		return resolved
	}
	if debug {
		log.Printf("[native] WARNING: could not resolve %s in any fallback stage", cmd)
	}
	return cmd
}

// spawn starts a new process and streams its stdout/stderr via events.
func (pt *processTracker) spawn(id string, cmd string, args []string, env map[string]string, cwd string) (string, error) {
	if id == "" {
		pt.mu.Lock()
		pt.nextID++
		id = fmt.Sprintf("proc-%d", pt.nextID)
		pt.mu.Unlock()
	}

	cmd = resolveExecutable(cmd, pt.debug)

	c := exec.Command(cmd, args...)
	if cwd != "" {
		c.Dir = cwd
	}
	c.Env = c.Environ()
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
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
		id:    id,
		cmd:   c,
		stdin: stdin,
		done:  make(chan struct{}),
	}

	pt.mu.Lock()
	pt.processes[id] = lp
	pt.mu.Unlock()

	if pt.debug {
		log.Printf("[native] spawned %s: %s %v (pid=%d, cwd=%s)", id, cmd, args, c.Process.Pid, cwd)
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

// streamOutput reads lines from a reader and emits stdout events.
// Both stdout and stderr from the child are forwarded as "stdout" events —
// the host loop only consumes stdout.
//
// On stderr, [SandboxDebug] lines (and any multi-line JSON they introduce)
// from sandbox-runtime are filtered out: they're noise from the wrapper, not
// the inner command.
func (pt *processTracker) streamOutput(id string, r io.Reader, stream string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	filterSRTDebugContinuation := false
	srtDebugBraceDepth := 0
	for scanner.Scan() {
		line := scanner.Text() + "\n"

		if stream == "stderr" {
			if filterSRTDebugContinuation {
				log.Printf("[native] %s srt-debug: %s", id, logx.Trunc(line))
				srtDebugBraceDepth += jsonBraceDelta(line)
				if srtDebugBraceDepth <= 0 {
					filterSRTDebugContinuation = false
				}
				continue
			}
			if payload, ok := sandboxRuntimeDebugPayload(line); ok {
				log.Printf("[native] %s srt-debug: %s", id, logx.Trunc(line))
				srtDebugBraceDepth = jsonBraceDelta(payload)
				filterSRTDebugContinuation = strings.HasPrefix(strings.TrimSpace(payload), "{") && srtDebugBraceDepth > 0
				continue
			}
		}

		if pt.debug {
			logx.Debug("[native] %s %s: %s", id, stream, logx.Trunc(line))
		}

		pt.emit(process.NewStdoutEvent(id, line))
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[native] %s %s scanner error: %v", id, stream, err)
		pt.emit(process.NewErrorEvent(id, fmt.Sprintf("%s scanner error: %v", stream, err), false))
	}
}

func sandboxRuntimeDebugPayload(line string) (string, bool) {
	payload, ok := strings.CutPrefix(line, "[SandboxDebug]")
	return payload, ok
}

func jsonBraceDelta(line string) int {
	return strings.Count(line, "{") - strings.Count(line, "}")
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

	pgid, err := syscall.Getpgid(lp.cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, sig)
	} else {
		_ = lp.cmd.Process.Signal(sig)
	}

	return nil
}

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

	select {
	case <-lp.done:
		return fmt.Errorf("process %s has exited", processID)
	default:
	}

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
