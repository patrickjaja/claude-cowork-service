package vm

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// QEMU launch parameters for a single VM instance.
type qemuLaunchSpec struct {
	bundleDir    string
	sessionDir   string
	rootDisk     string
	sessionData  string
	smolBinPath  string // optional
	kernel       string
	initrd       string
	monitorSock  string
	virtiofsSock string
	cid          uint32
	memoryGB     int
	cpus         int
}

// qemuInstance is a running QEMU process, launched with virtiofs + vsock.
type qemuInstance struct {
	cmd      *exec.Cmd
	pidFile  string
	mu       sync.Mutex
	running  bool
	exitedCh chan struct{}
}

func startQEMU(spec qemuLaunchSpec, debug bool) (*qemuInstance, error) {
	// virtiofs requires a shared memory backend for vhost-user-fs-pci.
	args := []string{
		"-enable-kvm",
		"-object", fmt.Sprintf("memory-backend-memfd,id=mem,size=%dG,share=on", spec.memoryGB),
		"-numa", "node,memdev=mem",
		"-m", fmt.Sprintf("%dG", spec.memoryGB),
		"-cpu", "host",
		"-smp", strconv.Itoa(spec.cpus),
		"-nographic",
	}

	// Direct kernel boot is required — the rootfs is built for Hyper-V and
	// has no BIOS-bootable MBR/GRUB, so falling through to SeaBIOS just
	// spins on iPXE forever. Fail loudly if vmlinuz/initrd are missing.
	if _, err := os.Stat(spec.kernel); err != nil {
		return nil, fmt.Errorf("kernel missing at %s: %w", spec.kernel, err)
	}
	if _, err := os.Stat(spec.initrd); err != nil {
		return nil, fmt.Errorf("initrd missing at %s: %w", spec.initrd, err)
	}
	if debug {
		log.Printf("[kvm] direct kernel boot: kernel=%s initrd=%s", spec.kernel, spec.initrd)
	}
	args = append(args,
		"-kernel", spec.kernel,
		"-initrd", spec.initrd,
		"-append", "root=LABEL=cloudimg-rootfs console=ttyS0 quiet",
	)

	// Rootfs → /dev/vda. Mounted writable: matches Windows behaviour where
	// the rootfs persists between boots, so apt-installed packages and other
	// system-state edits survive a stop/start cycle.
	args = append(args,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", spec.rootDisk),
	)

	// Session disk → /dev/vdb (formatted by guest sdk-daemon on first boot).
	args = append(args,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", spec.sessionData),
	)

	// smol-bin disk → /dev/vdc, optional.
	if spec.smolBinPath != "" {
		args = append(args,
			"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio,readonly=on", spec.smolBinPath),
		)
	}

	// vsock + QMP + user-mode networking.
	args = append(args,
		"-device", fmt.Sprintf("vhost-vsock-pci,guest-cid=%d", spec.cid),
		"-qmp", fmt.Sprintf("unix:%s,server,nowait", spec.monitorSock),
		"-netdev", "user,id=net0",
		"-machine", "type=q35",
		"-device", "virtio-net-pci,netdev=net0",
	)

	// Per-session virtiofs share.
	args = append(args,
		"-chardev", fmt.Sprintf("socket,id=virtiofs,path=%s", spec.virtiofsSock),
		"-device", fmt.Sprintf("vhost-user-fs-pci,chardev=virtiofs,tag=%s", VFSShareMountTag),
	)

	cmd := exec.Command("qemu-system-x86_64", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Detach QEMU into its own process group so shell/systemd SIGTERM
	// doesn't hit it directly — StopVM drives ACPI powerdown instead.
	// Pdeathsig ensures QEMU dies if the daemon is SIGKILLed and can't
	// run its normal teardown path.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}

	if debug {
		log.Printf("[kvm] starting QEMU: qemu-system-x86_64 %s", strings.Join(args, " "))
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting QEMU: %w", err)
	}

	inst := &qemuInstance{
		cmd:      cmd,
		pidFile:  filepath.Join(spec.sessionDir, "qemu.pid"),
		running:  true,
		exitedCh: make(chan struct{}),
	}
	if err := os.WriteFile(inst.pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		log.Printf("[kvm] writing QEMU PID file %s: %v", inst.pidFile, err)
	}

	log.Printf("[kvm] QEMU started (PID %d, CID %d)", cmd.Process.Pid, spec.cid)

	// Quick health check: QEMU exits within ~100ms on fatal launch errors.
	time.Sleep(500 * time.Millisecond)
	if !inst.isAlive() {
		inst.running = false
		close(inst.exitedCh)
		if err := os.Remove(inst.pidFile); err != nil && !os.IsNotExist(err) {
			log.Printf("[kvm] remove PID file %s: %v", inst.pidFile, err)
		}
		return nil, fmt.Errorf("QEMU exited immediately (check disk image or KVM access)")
	}

	go func() {
		err := cmd.Wait()
		inst.mu.Lock()
		inst.running = false
		inst.mu.Unlock()
		if rerr := os.Remove(inst.pidFile); rerr != nil && !os.IsNotExist(rerr) {
			log.Printf("[kvm] remove PID file %s: %v", inst.pidFile, rerr)
		}
		close(inst.exitedCh)
		if err != nil {
			log.Printf("[kvm] QEMU exited with error: %v", err)
		} else {
			log.Printf("[kvm] QEMU exited cleanly")
		}
	}()

	return inst, nil
}

func (q *qemuInstance) isAlive() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.running || q.cmd == nil || q.cmd.Process == nil {
		return false
	}
	return q.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// IsRunning reports whether the QEMU process is still alive.
func (q *qemuInstance) IsRunning() bool {
	q.mu.Lock()
	running := q.running
	q.mu.Unlock()
	if !running {
		return false
	}
	return q.isAlive()
}

// Shutdown attempts ACPI shutdown via QMP, then force-quit via QMP, then
// SIGKILL. Returns after the process has actually exited (or 15s elapses).
func (q *qemuInstance) Shutdown(qmp *QmpClient) {
	q.mu.Lock()
	if !q.running || q.cmd == nil || q.cmd.Process == nil {
		q.mu.Unlock()
		return
	}
	pid := q.cmd.Process.Pid
	q.mu.Unlock()

	log.Printf("[kvm] stopping QEMU (PID %d)", pid)

	if qmp != nil {
		if err := qmp.Execute("system_powerdown"); err != nil {
			log.Printf("[kvm] ACPI powerdown failed: %v", err)
		}
	}

	select {
	case <-q.exitedCh:
		return
	case <-time.After(10 * time.Second):
	}

	if qmp != nil {
		qmp.Execute("quit") //nolint:errcheck
	}
	select {
	case <-q.exitedCh:
		return
	case <-time.After(3 * time.Second):
	}

	// Last resort.
	if err := q.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		log.Printf("[kvm] SIGKILL QEMU: %v", err)
	}
	select {
	case <-q.exitedCh:
	case <-time.After(2 * time.Second):
	}
}

// killStalePID kills any leftover QEMU process from a previous run using its
// PID file. Called before launching a fresh VM to avoid disk image locks.
func killStalePID(stateDir string) {
	pidFile := filepath.Join(stateDir, "qemu.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidFile) // malformed pidfile — nothing to recover
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidFile)
		return
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		_ = os.Remove(pidFile) // process already gone
		return
	}
	log.Printf("[kvm] killing stale QEMU (PID %d)", pid)
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		log.Printf("[kvm] SIGTERM stale QEMU %d: %v", pid, err)
	}
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			_ = os.Remove(pidFile)
			return
		}
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		log.Printf("[kvm] SIGKILL stale QEMU %d: %v", pid, err)
	}
	time.Sleep(200 * time.Millisecond)
	_ = os.Remove(pidFile)
}

// vhdxConvertedCanary is appended to a VHDX after we successfully convert
// it to qcow2. On subsequent boots, presence of this trailer means the
// sibling qcow2 is up to date. If Claude Desktop ships a fresh VHDX, the
// canary is gone and we reconvert. Hashing multi-GB VHDX files on every
// startup was the previous strategy and was unacceptably slow.
var vhdxConvertedCanary = []byte("\x00COWORK-VHDX-CONVERTED-V1\x00")

func vhdxHasCanary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.Size() < int64(len(vhdxConvertedCanary)) {
		return false
	}
	buf := make([]byte, len(vhdxConvertedCanary))
	if _, err := f.ReadAt(buf, fi.Size()-int64(len(buf))); err != nil {
		return false
	}
	return bytes.Equal(buf, vhdxConvertedCanary)
}

func appendVhdxCanary(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, werr := f.Write(vhdxConvertedCanary); werr != nil {
		_ = f.Close()
		return werr
	}
	return f.Close()
}

// ensureVHDXConverted converts <basename>.vhdx → <basename>.qcow2 in
// bundleDir, using a trailer canary on the source VHDX as the cache key.
// Returns the qcow2 path. If the VHDX is missing but a qcow2 is already
// present, that qcow2 is reused (supports users shipping only qcow2).
func ensureVHDXConverted(bundleDir, basename string) (string, error) {
	vhdxPath := filepath.Join(bundleDir, basename+".vhdx")
	qcow2Path := filepath.Join(bundleDir, basename+".qcow2")

	_, vhdxErr := os.Stat(vhdxPath)
	_, qcow2Err := os.Stat(qcow2Path)

	if vhdxErr != nil {
		if qcow2Err == nil {
			return qcow2Path, nil
		}
		return "", fmt.Errorf("%s not found: %w", vhdxPath, vhdxErr)
	}

	if qcow2Err == nil && vhdxHasCanary(vhdxPath) {
		return qcow2Path, nil
	}

	if qcow2Err == nil {
		log.Printf("[kvm] %s.vhdx canary missing — reconverting", basename)
		if err := os.Remove(qcow2Path); err != nil && !os.IsNotExist(err) {
			log.Printf("[kvm] removing stale qcow2 %s: %v", qcow2Path, err)
		}
	}

	log.Printf("[kvm] converting %s.vhdx → qcow2 (this is slow on first run)", basename)
	cmd := exec.Command("qemu-img", "convert",
		"-f", "vhdx", "-O", "qcow2", vhdxPath, qcow2Path)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Clean up the partial qcow2. Ignore the Remove error — the conversion
		// error we return is more actionable, and the file will be overwritten
		// on retry.
		_ = os.Remove(qcow2Path)
		return "", fmt.Errorf("converting %s.vhdx: %s: %w",
			basename, strings.TrimSpace(string(out)), err)
	}
	if err := appendVhdxCanary(vhdxPath); err != nil {
		log.Printf("[kvm] could not stamp canary on %s.vhdx: %v (will reconvert next run)",
			basename, err)
	}
	return qcow2Path, nil
}

// findSmolBin returns an up-to-date smol-bin qcow2 path in the given dirs,
// trying both "smol-bin" and the arch-specific "smol-bin.$GOARCH" that
// Claude Desktop ships. Conversion goes through ensureVHDXConverted so
// checksum-based cache invalidation applies. Returns "" (with no error)
// if no smol-bin source is available — not every session needs one.
func findSmolBin(dirs []string) string {
	archTag := "x64"
	if runtime.GOARCH == "arm64" {
		archTag = "arm64"
	}
	for _, d := range dirs {
		for _, name := range []string{"smol-bin", "smol-bin." + archTag} {
			qcow, err := ensureVHDXConverted(d, name)
			if err != nil {
				// missing source + no cached qcow2 is the common case
				// for the bases we didn't ship; just try the next name.
				continue
			}
			return qcow
		}
	}
	return ""
}

// ensureSessionDataDisk creates a 2G qcow2 file if missing.
func ensureSessionDataDisk(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", path, "2G")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating session disk: %s: %w",
			strings.TrimSpace(string(out)), err)
	}
	return nil
}
