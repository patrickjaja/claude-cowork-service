package vm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// VfsHelper wraps the child vfs-mount-helper process that owns the virtiofsd
// socket and the shared staging area. Bind mounts added at runtime become
// visible to the guest live because virtiofsd runs inside the same user +
// mount namespace as the helper.
type VfsHelper struct {
	stagingDir string
	socketPath string

	cmd   *exec.Cmd
	stdin io.WriteCloser
	ready chan struct{}
	exit  chan struct{}

	mu          sync.Mutex
	readyFired  bool
	readyErr    error
	nextReqID   uint64
	pending     map[string]chan helperResp
	activeBinds map[string]struct{}
	debug       bool
}

type helperResp struct {
	OK    bool
	Error string
}

// NewVfsHelper configures (but does not start) a helper process. Call Start
// to launch it and wait for the virtiofsd socket to be ready.
func NewVfsHelper(stagingDir, socketPath string, debug bool) *VfsHelper {
	return &VfsHelper{
		stagingDir:  stagingDir,
		socketPath:  socketPath,
		ready:       make(chan struct{}),
		exit:        make(chan struct{}),
		pending:     make(map[string]chan helperResp),
		activeBinds: make(map[string]struct{}),
		debug:       debug,
	}
}

// Start spawns the helper, which re-execs the current binary with
// `--vfs-helper` inside `unshare --user --map-root-user --mount`, and blocks
// until the helper reports the virtiofsd socket is ready or timeout elapses.
func (v *VfsHelper) Start(timeout time.Duration) error {
	selfExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating self executable: %w", err)
	}

	args := []string{
		"--user", "--map-root-user", "--mount",
		"--propagation=slave",
		"--", selfExe, "--vfs-helper",
		"--staging", v.stagingDir,
		"--socket", v.socketPath,
	}
	if v.debug {
		log.Printf("[kvm] launching vfs helper: unshare %v", args)
	}
	v.cmd = exec.Command("unshare", args...)
	// Keep the helper (and virtiofsd it spawns) out of our process group
	// so SIGTERM from the shell/systemd doesn't tear virtiofsd down before
	// StopVM gets a chance to do an ACPI powerdown on QEMU. Pdeathsig
	// still makes the helper die if the daemon is SIGKILLed.
	v.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}

	stdin, err := v.cmd.StdinPipe()
	if err != nil {
		return err
	}
	v.stdin = stdin
	stdout, err := v.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := v.cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := v.cmd.Start(); err != nil {
		return fmt.Errorf("starting vfs helper: %w", err)
	}

	go v.readStdout(stdout)
	go v.drainStderr(stderr)
	go v.watchExit()

	select {
	case <-v.ready:
		if v.readyErr != nil {
			return v.readyErr
		}
		return nil
	case <-time.After(timeout):
		v.Stop()
		return fmt.Errorf("vfs-helper did not become ready within %s", timeout)
	case <-v.exit:
		return fmt.Errorf("vfs-helper exited before ready")
	}
}

func (v *VfsHelper) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		log.Printf("%s", sc.Text())
	}
}

func (v *VfsHelper) watchExit() {
	err := v.cmd.Wait()
	if v.debug {
		log.Printf("[kvm] vfs-helper exited: %v", err)
	}
	close(v.exit)
	// Reject any pending commands.
	v.mu.Lock()
	for id, ch := range v.pending {
		ch <- helperResp{OK: false, Error: "vfs-helper exited"}
		close(ch)
		delete(v.pending, id)
	}
	v.mu.Unlock()
}

func (v *VfsHelper) readStdout(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		v.handleLine(sc.Bytes())
	}
}

func (v *VfsHelper) handleLine(line []byte) {
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		if v.debug {
			log.Printf("[kvm] vfs-helper unparseable: %s", string(line))
		}
		return
	}
	if ev, ok := msg["event"]; ok {
		var eventName string
		if err := json.Unmarshal(ev, &eventName); err != nil {
			log.Printf("[kvm] VFS helper event unmarshal: %v", err)
			return
		}
		switch eventName {
		case "ready":
			v.mu.Lock()
			if !v.readyFired {
				v.readyFired = true
				close(v.ready)
			}
			v.mu.Unlock()
		case "virtiofsd-exit":
			log.Printf("[kvm] virtiofsd died: %s", string(line))
		}
		return
	}
	if idRaw, ok := msg["id"]; ok {
		id := normalizeID(idRaw)
		v.mu.Lock()
		ch, found := v.pending[id]
		if found {
			delete(v.pending, id)
		}
		v.mu.Unlock()
		if found {
			okField := msg["ok"]
			errField := msg["error"]
			var okVal bool
			if err := json.Unmarshal(okField, &okVal); err != nil && len(okField) > 0 {
				log.Printf("[kvm] VFS helper resp: ok field unmarshal: %v", err)
			}
			var errStr string
			if err := json.Unmarshal(errField, &errStr); err != nil && len(errField) > 0 {
				log.Printf("[kvm] VFS helper resp: error field unmarshal: %v", err)
			}
			ch <- helperResp{OK: okVal, Error: errStr}
			close(ch)
			return
		}
	}
}

func (v *VfsHelper) send(cmd map[string]interface{}) error {
	v.mu.Lock()
	if v.nextReqID == 0 {
		v.nextReqID = 1
	}
	id := strconv.FormatUint(v.nextReqID, 10)
	v.nextReqID++
	ch := make(chan helperResp, 1)
	v.pending[id] = ch
	v.mu.Unlock()

	cmd["id"] = id
	data, err := json.Marshal(cmd)
	if err != nil {
		v.mu.Lock()
		delete(v.pending, id)
		v.mu.Unlock()
		return err
	}
	data = append(data, '\n')
	if _, err := v.stdin.Write(data); err != nil {
		v.mu.Lock()
		delete(v.pending, id)
		v.mu.Unlock()
		return err
	}

	select {
	case resp := <-ch:
		if !resp.OK {
			return fmt.Errorf("%s", resp.Error)
		}
		return nil
	case <-time.After(10 * time.Second):
		v.mu.Lock()
		delete(v.pending, id)
		v.mu.Unlock()
		return fmt.Errorf("vfs-helper command timed out: %v", cmd["op"])
	}
}

// Bind adds an idempotent bind mount under the staging area. relPath is the
// host absolute path minus the leading slash (matches what the guest
// sdk-daemon expects under /mnt/.virtiofs-root/shared/). mode is "rw",
// "rwd", or "ro" — empty defaults to "rw".
func (v *VfsHelper) Bind(relPath, mode string) error {
	v.mu.Lock()
	if _, ok := v.activeBinds[relPath]; ok {
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()

	hostPath, err := hostAbsFromShared(relPath)
	if err != nil {
		return err
	}
	if mode == "" {
		mode = "rw"
	}
	if err := v.send(map[string]interface{}{
		"op": "bind", "hostPath": hostPath, "relPath": relPath, "mode": mode,
	}); err != nil {
		return err
	}
	v.mu.Lock()
	v.activeBinds[relPath] = struct{}{}
	v.mu.Unlock()
	return nil
}

// Stop asks the helper to tear down all binds and exit virtiofsd, then waits
// up to 3s for the helper to exit before SIGKILLing.
func (v *VfsHelper) Stop() {
	if v.cmd == nil || v.cmd.Process == nil {
		return
	}
	// Best-effort graceful stop.
	v.send(map[string]interface{}{"op": "stop"}) //nolint:errcheck
	if v.stdin != nil {
		if err := v.stdin.Close(); err != nil {
			log.Printf("[kvm] close VFS helper stdin: %v", err)
		}
	}
	select {
	case <-v.exit:
		return
	case <-time.After(3 * time.Second):
	}
	if err := v.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("[kvm] SIGTERM VFS helper: %v", err)
	}
	select {
	case <-v.exit:
		return
	case <-time.After(2 * time.Second):
	}
	if err := v.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		log.Printf("[kvm] SIGKILL VFS helper: %v", err)
	}
}
