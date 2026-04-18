package vm

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// POSIX access(2) mode bits. syscall doesn't export them on Linux.
const (
	rOK = 4
	wOK = 2
)

// PreflightResult describes whether the KVM backend can start here.
type PreflightResult struct {
	OK     bool
	Reason string
}

// CheckKvmPrerequisites verifies everything the KvmBackend needs: KVM/vsock
// devices, qemu, virtiofsd, and unprivileged user namespaces (the VFS mount
// helper relies on those for unprivileged bind mounts under the staging
// dir). Returns the first missing prerequisite with a remediation hint.
func CheckKvmPrerequisites() PreflightResult {
	if err := syscall.Access("/dev/kvm", rOK|wOK); err != nil {
		return PreflightResult{
			Reason: fmt.Sprintf("/dev/kvm not accessible: %v", err),
		}
	}
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		return PreflightResult{
			Reason: "qemu-system-x86_64 not found in PATH",
		}
	}
	if err := syscall.Access("/dev/vhost-vsock", rOK); err != nil {
		return PreflightResult{
			Reason: fmt.Sprintf("/dev/vhost-vsock not accessible: %v", err),
		}
	}
	if _, err := exec.LookPath("virtiofsd"); err != nil {
		return PreflightResult{
			Reason: "virtiofsd not found in PATH (install qemu-virtiofsd or the rust-vmm virtiofsd package)",
		}
	}
	// Verify unprivileged user namespaces actually work by running a tiny
	// unshare+true. Some distros (Ubuntu 24.04 AppArmor, kernels with
	// kernel.unprivileged_userns_clone=0) block this even when the binary
	// is present.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--mount", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return PreflightResult{
			Reason: fmt.Sprintf("unprivileged user namespaces unavailable: %v (%s). "+
				"Enable with `sudo sysctl -w kernel.unprivileged_userns_clone=1` "+
				"(or relax AppArmor on Ubuntu 24.04+ via "+
				"kernel.apparmor_restrict_unprivileged_userns=0)",
				err, shortErr(out)),
		}
	}
	return PreflightResult{OK: true}
}

func shortErr(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
