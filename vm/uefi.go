package vm

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Boot strategy for a VM rootfs.
//
//   - bootDirectKernel: legacy Hyper-V rootfs (rootfs.vhdx / rootfs.qcow2). The
//     image has no BIOS-bootable bootloader, so QEMU is given an external
//     -kernel/-initrd and direct-boots the guest.
//   - bootUEFIDisk: native Linux cloud image (rootfs.img) shipped in the
//     upstream "unix" VM bundle as of Claude Desktop v1.17282. It is a full
//     GPT disk with an ESP (GRUB) + BIOS-boot partition, so it boots itself
//     under OVMF/UEFI with no external kernel/initrd.
type bootMode int

const (
	bootDirectKernel bootMode = iota
	bootUEFIDisk
)

// ovmfCodePaths / ovmfVarsTemplatePaths list the per-distro locations of the
// split OVMF firmware (CODE = read-only executable, VARS = writable NVRAM
// template). First existing match wins. The env override
// COWORK_OVMF_CODE / COWORK_OVMF_VARS takes precedence over all of these.
// Path lists verified against current distro packaging (2026):
//   - Arch (edk2-ovmf): /usr/share/edk2/x64/OVMF_{CODE,VARS}.4m.fd
//   - Debian 12+ / Ubuntu 24.04+ (ovmf): only the 4M variants ship now
//     (/usr/share/OVMF/OVMF_{CODE,VARS}_4M.fd); the old 2M OVMF_CODE.fd was
//     dropped. Kept as a fallback for older Debian/Ubuntu.
//   - Fedora 40+ / RHEL 9+ (edk2-ovmf): populates BOTH /usr/share/edk2/ovmf/
//     and /usr/share/OVMF/ with plain OVMF_{CODE,VARS}.fd (no _4M suffix).
// Distros move these around between releases, so this is best-effort: when
// none match, StartVM fails with an actionable error naming COWORK_OVMF_CODE /
// COWORK_OVMF_VARS, which the README documents.
var ovmfCodePaths = []string{
	"/usr/share/edk2/x64/OVMF_CODE.4m.fd",      // Arch (edk2-ovmf)
	"/usr/share/edk2-ovmf/x64/OVMF_CODE.4m.fd", // Arch (older split pkg)
	"/usr/share/OVMF/OVMF_CODE_4M.fd",          // Debian 12+ / Ubuntu 24.04+
	"/usr/share/edk2/ovmf/OVMF_CODE.fd",        // Fedora 40+ / RHEL 9+
	"/usr/share/OVMF/OVMF_CODE.fd",             // Fedora (also here); old Debian/Ubuntu
	"/usr/share/qemu/OVMF_CODE.fd",             // misc
}

var ovmfVarsTemplatePaths = []string{
	"/usr/share/edk2/x64/OVMF_VARS.4m.fd",      // Arch
	"/usr/share/edk2-ovmf/x64/OVMF_VARS.4m.fd", // Arch (older split pkg)
	"/usr/share/OVMF/OVMF_VARS_4M.fd",          // Debian 12+ / Ubuntu 24.04+
	"/usr/share/edk2/ovmf/OVMF_VARS.fd",        // Fedora 40+ / RHEL 9+
	"/usr/share/OVMF/OVMF_VARS.fd",             // Fedora (also here); old Debian/Ubuntu
	"/usr/share/qemu/OVMF_VARS.fd",             // misc
}

// nativeRootfsImageName is the raw UEFI-bootable disk image shipped in the
// upstream "unix" bundle (darwin + linux). Distinct from the Hyper-V
// rootfs.vhdx / rootfs.qcow2.
const nativeRootfsImageName = "rootfs.img"

// rootfsImagePath returns the path to a native rootfs.img in bundleDir, or ""
// if absent.
func rootfsImagePath(bundleDir string) string {
	p := filepath.Join(bundleDir, nativeRootfsImageName)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// firstExisting returns the first path that exists, honoring an env override.
func firstExisting(envVar string, paths []string) string {
	if v := os.Getenv(envVar); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
		log.Printf("[kvm] %s=%s does not exist, falling back to autodetect", envVar, v)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findOVMFCode resolves the read-only OVMF firmware image.
func findOVMFCode() string { return firstExisting("COWORK_OVMF_CODE", ovmfCodePaths) }

// findOVMFVarsTemplate resolves the OVMF NVRAM template (copied per-session).
func findOVMFVarsTemplate() string { return firstExisting("COWORK_OVMF_VARS", ovmfVarsTemplatePaths) }

// ensureOVMFVars copies the OVMF_VARS template into sessionDir, producing a
// writable per-VM NVRAM file. UEFI needs writable NVRAM, and the template is
// root-owned/read-only, so each VM gets its own copy.
func ensureOVMFVars(sessionDir, template string) (string, error) {
	dst := filepath.Join(sessionDir, "OVMF_VARS.fd")
	if _, err := os.Stat(dst); err == nil {
		return dst, nil // already prepared (resume)
	}
	in, err := os.Open(template)
	if err != nil {
		return "", fmt.Errorf("opening OVMF VARS template %s: %w", template, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("creating OVMF VARS %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return "", fmt.Errorf("copying OVMF VARS: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("closing OVMF VARS: %w", err)
	}
	return dst, nil
}

// ensureNativeRootOverlay builds a qcow2 copy-on-write overlay backed by the
// pristine rootfs.img. Writes (apt installs, system-state edits) land in the
// overlay and persist across stop/start, while the multi-GB downloaded image
// stays untouched, so re-verifying its checksum or re-downloading is never
// needed. Mirrors the persistence semantics of the vhdx→qcow2 path.
func ensureNativeRootOverlay(bundleDir, imgPath string) (string, error) {
	overlay := filepath.Join(bundleDir, "rootfs.img.overlay.qcow2")
	if _, err := os.Stat(overlay); err == nil {
		return overlay, nil
	}
	cmd := exec.Command("qemu-img", "create",
		"-f", "qcow2", "-F", "raw", "-b", imgPath, overlay)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(overlay)
		return "", fmt.Errorf("creating rootfs.img overlay: %s: %w",
			strings.TrimSpace(string(out)), err)
	}
	return overlay, nil
}

// hostQEMUSystemBinary is the QEMU system emulator for the host architecture.
func hostQEMUSystemBinary() string {
	if runtime.GOARCH == "arm64" {
		return "qemu-system-aarch64"
	}
	return "qemu-system-x86_64"
}
