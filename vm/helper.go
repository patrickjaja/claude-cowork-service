package vm

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RunVfsHelper is the entry point for the VFS mount helper mode. The main
// cowork-svc-linux binary re-execs itself with `--vfs-helper --staging DIR
// --socket PATH` inside `unshare --user --map-root-user --mount
// --propagation=slave`, giving the process root UID inside its own user
// namespace so it can perform unprivileged bind mounts.
//
// Protocol — newline-delimited JSON on stdin/stdout:
//
//	request:  {id, op: "bind",   hostPath, relPath, mode}
//	request:  {id, op: "unbind", relPath}
//	request:  {id, op: "stop"}
//	response: {id, ok: true}  |  {id, ok: false, error}
//	event:    {event: "ready"}
//	event:    {event: "virtiofsd-exit", code, signal}
//
// Returns the exit code the parent binary should use.
func RunVfsHelper(args []string) int {
	fs := flag.NewFlagSet("vfs-helper", flag.ContinueOnError)
	staging := fs.String("staging", "", "staging directory for virtiofs share")
	socket := fs.String("socket", "", "virtiofsd socket path")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	if *staging == "" || *socket == "" {
		fmt.Fprintf(os.Stderr, "usage: --vfs-helper --staging DIR --socket PATH\n")
		return 2
	}

	h := &vfsHelper{
		stagingDir:  *staging,
		sharedRoot:  filepath.Join(*staging, "shared"),
		socketPath:  *socket,
		activeBinds: make(map[string]struct{}),
	}
	return h.run()
}

type vfsHelper struct {
	stagingDir  string
	sharedRoot  string
	socketPath  string
	activeBinds map[string]struct{}
	mu          sync.Mutex

	virtiofsd *exec.Cmd
	stopping  bool

	encMu sync.Mutex // serializes writes to stdout
}

type helperRequest struct {
	ID       interface{} `json:"id"`
	Op       string      `json:"op"`
	HostPath string      `json:"hostPath,omitempty"`
	RelPath  string      `json:"relPath,omitempty"`
	Mode     string      `json:"mode,omitempty"`
}

func (h *vfsHelper) log(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[vfs-helper] "+format+"\n", args...)
}

func (h *vfsHelper) emit(obj map[string]interface{}) {
	h.encMu.Lock()
	defer h.encMu.Unlock()
	data, err := json.Marshal(obj)
	if err != nil {
		return
	}
	// Stdout is the helper's protocol channel back to the parent. If the
	// parent has closed the pipe we're about to exit anyway — no useful
	// recovery.
	_, _ = os.Stdout.Write(append(data, '\n'))
}

func (h *vfsHelper) run() int {
	if err := os.MkdirAll(h.sharedRoot, 0o755); err != nil {
		h.log("mkdir sharedRoot: %v", err)
		return 1
	}

	// We run inside `unshare --map-root-user`, which only maps the caller's
	// host UID/GID into the namespace as 0. Any guest-side chown / mkdir
	// that tries to use a UID outside that single mapped slot returns
	// EINVAL. Squash all guest UIDs/GIDs to 0 (IS the mapped slot,
	// representing the host UID virtiofsd actually runs as), and pass host
	// UID 0 back as guest 0, so ownership is consistent in both
	// directions and every metadata op stays inside the userns mapping.
	h.virtiofsd = exec.Command(FindVirtiofsd(),
		"--socket-path="+h.socketPath,
		"--shared-dir", h.stagingDir,
		"--cache=auto",
		"--inode-file-handles=never",
		"--sandbox=none",
		"--translate-uid", "squash-guest:0:0:65536",
		"--translate-uid", "squash-host:0:0:1",
		"--translate-gid", "squash-guest:0:0:65536",
		"--translate-gid", "squash-host:0:0:1",
	)
	stdoutPipe, _ := h.virtiofsd.StdoutPipe()
	stderrPipe, _ := h.virtiofsd.StderrPipe()

	if err := h.virtiofsd.Start(); err != nil {
		h.log("virtiofsd spawn error: %v", err)
		h.emit(map[string]interface{}{
			"event": "virtiofsd-exit",
			"code":  nil, "signal": nil, "error": err.Error(),
		})
		return 1
	}
	go copyWithPrefix("[virtiofsd] ", stdoutPipe)
	go copyWithPrefix("[virtiofsd] ", stderrPipe)

	// Watch for virtiofsd exit and propagate.
	exitCh := make(chan struct{})
	go func() {
		err := h.virtiofsd.Wait()
		code, signal := exitDetails(err)
		h.log("virtiofsd exited: code=%v, signal=%v", code, signal)
		h.emit(map[string]interface{}{
			"event": "virtiofsd-exit", "code": code, "signal": signal,
		})
		close(exitCh)
	}()

	// Wait up to 10s for the socket to appear, then emit ready.
	go func() {
		start := time.Now()
		for time.Since(start) < 10*time.Second {
			if _, err := os.Stat(h.socketPath); err == nil {
				h.log("socket ready after %dms", time.Since(start).Milliseconds())
				h.emit(map[string]interface{}{"event": "ready"})
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		h.log("socket never appeared")
		if err := h.virtiofsd.Process.Signal(syscall.SIGTERM); err != nil {
			h.log("SIGTERM virtiofsd: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Goroutine: read stdin line by line.
	lineCh := make(chan []byte, 16)
	go func() {
		defer close(lineCh)
		r := bufio.NewReader(os.Stdin)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				lineCh <- line
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case line, ok := <-lineCh:
			if !ok {
				h.log("stdin closed")
				h.doStop()
				<-exitCh
				return 0
			}
			h.handleLine(line)
		case <-sigCh:
			h.doStop()
			<-exitCh
			return 0
		case <-exitCh:
			if code, _ := exitDetails(nil); code == 0 {
				return 0
			}
			return 1
		}
	}
}

func (h *vfsHelper) handleLine(line []byte) {
	line = trimLine(line)
	if len(line) == 0 {
		return
	}
	var req helperRequest
	if err := json.Unmarshal(line, &req); err != nil {
		h.emit(map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("invalid JSON: %v", err),
		})
		return
	}
	switch req.Op {
	case "bind":
		if err := h.doBind(req); err != nil {
			h.emit(map[string]interface{}{"id": req.ID, "ok": false, "error": err.Error()})
			return
		}
		h.emit(map[string]interface{}{"id": req.ID, "ok": true})
	case "unbind":
		h.doUnbind(req.RelPath)
		h.emit(map[string]interface{}{"id": req.ID, "ok": true})
	case "stop":
		h.emit(map[string]interface{}{"id": req.ID, "ok": true})
		h.doStop()
	default:
		h.emit(map[string]interface{}{
			"id": req.ID, "ok": false,
			"error": fmt.Sprintf("unknown op: %s", req.Op),
		})
	}
}

func (h *vfsHelper) validateRelPath(relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("relPath required")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("relPath must be relative: %s", relPath)
	}
	target := filepath.Clean(filepath.Join(h.sharedRoot, relPath))
	if target != h.sharedRoot && !strings.HasPrefix(target, h.sharedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("relPath escapes shared root: %s", relPath)
	}
	return target, nil
}

func (h *vfsHelper) doBind(req helperRequest) error {
	if req.HostPath == "" || !filepath.IsAbs(req.HostPath) {
		return fmt.Errorf("hostPath must be absolute: %s", req.HostPath)
	}
	target, err := h.validateRelPath(req.RelPath)
	if err != nil {
		return err
	}

	h.mu.Lock()
	if _, ok := h.activeBinds[req.RelPath]; ok {
		h.mu.Unlock()
		return nil // idempotent
	}
	h.mu.Unlock()

	// rw/rwd binds may need the source created (e.g. SDK install dir).
	// ro binds require the source to already exist.
	info, err := os.Stat(req.HostPath)
	if err != nil {
		if os.IsNotExist(err) && (req.Mode == "rw" || req.Mode == "rwd") {
			if err := os.MkdirAll(req.HostPath, 0o755); err != nil {
				return fmt.Errorf("mkdir hostPath: %w", err)
			}
			info, err = os.Stat(req.HostPath)
			if err != nil {
				return fmt.Errorf("stat hostPath after mkdir: %w", err)
			}
		} else {
			return fmt.Errorf("hostPath missing: %s", req.HostPath)
		}
	}
	if info != nil && !info.IsDir() {
		h.log("skip non-directory bind %s", req.HostPath)
		return nil
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}

	if out, err := exec.Command("mount", "--bind", req.HostPath, target).CombinedOutput(); err != nil {
		return fmt.Errorf("mount --bind failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if req.Mode == "ro" {
		if out, err := exec.Command("mount", "-o", "remount,ro,bind", req.HostPath, target).CombinedOutput(); err != nil {
			return fmt.Errorf("remount ro failed: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	h.mu.Lock()
	h.activeBinds[req.RelPath] = struct{}{}
	h.mu.Unlock()
	mode := req.Mode
	if mode == "" {
		mode = "rw"
	}
	h.log("bind %s -> %s (%s)", req.HostPath, target, mode)
	return nil
}

func (h *vfsHelper) doUnbind(relPath string) {
	target, err := h.validateRelPath(relPath)
	if err != nil {
		return
	}
	h.mu.Lock()
	_, bound := h.activeBinds[relPath]
	h.mu.Unlock()
	if !bound {
		return
	}
	if out, err := exec.Command("umount", "-l", target).CombinedOutput(); err != nil {
		h.log("umount %s: %v (%s)", relPath, err, strings.TrimSpace(string(out)))
	}
	h.mu.Lock()
	delete(h.activeBinds, relPath)
	h.mu.Unlock()
}

func (h *vfsHelper) doStop() {
	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return
	}
	h.stopping = true
	relPaths := make([]string, 0, len(h.activeBinds))
	for k := range h.activeBinds {
		relPaths = append(relPaths, k)
	}
	h.mu.Unlock()

	// Unbind in reverse order so nested binds unwind cleanly.
	for i := len(relPaths) - 1; i >= 0; i-- {
		h.doUnbind(relPaths[i])
	}

	if h.virtiofsd != nil && h.virtiofsd.Process != nil {
		if err := h.virtiofsd.Process.Signal(syscall.SIGTERM); err != nil {
			h.log("SIGTERM virtiofsd: %v", err)
		}
		go func() {
			time.Sleep(2 * time.Second)
			if err := h.virtiofsd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				h.log("SIGKILL virtiofsd: %v", err)
			}
		}()
	}
}

func copyWithPrefix(prefix string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Fprintf(os.Stderr, "%s%s\n", prefix, sc.Text())
	}
}

// exitDetails extracts the exit code and signal from a cmd.Wait error.
// Returns code=0 signal="" on normal exit.
func exitDetails(err error) (interface{}, interface{}) {
	if err == nil {
		return 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return nil, ws.Signal().String()
			}
			return ws.ExitStatus(), nil
		}
		return ee.ExitCode(), nil
	}
	return nil, nil
}

func trimLine(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}
