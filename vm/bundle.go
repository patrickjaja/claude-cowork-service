package vm

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// BundleManager handles VM image bundles (download, convert, cache).
type BundleManager struct {
	dataDir string
	debug   bool
}

// NewBundleManager creates a new bundle manager.
func NewBundleManager(dataDir string, debug bool) *BundleManager {
	return &BundleManager{
		dataDir: dataDir,
		debug:   debug,
	}
}

// BundleDir returns the path to a specific bundle by SHA.
func (b *BundleManager) BundleDir(sha string) string {
	return filepath.Join(b.dataDir, "bundles", sha)
}

// BundleExists checks if a bundle has been downloaded and converted.
func (b *BundleManager) BundleExists(sha string) bool {
	dir := b.BundleDir(sha)
	required := []string{"vmlinuz", "initrd", "rootfs.qcow2"}
	for _, f := range required {
		if _, err := os.Stat(filepath.Join(dir, f)); os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// ConvertVHDX converts a VHDX file to qcow2 format using qemu-img.
// The VHDX format is used by Windows/Hyper-V; QEMU needs qcow2.
func (b *BundleManager) ConvertVHDX(bundleDir string) error {
	vhdxPath := filepath.Join(bundleDir, "rootfs.vhdx")
	qcow2Path := filepath.Join(bundleDir, "rootfs.qcow2")

	// Check if already converted
	if _, err := os.Stat(qcow2Path); err == nil {
		if b.debug {
			log.Printf("rootfs.qcow2 already exists in %s", bundleDir)
		}
		return nil
	}

	// Check if VHDX exists
	if _, err := os.Stat(vhdxPath); os.IsNotExist(err) {
		return fmt.Errorf("rootfs.vhdx not found in %s", bundleDir)
	}

	log.Printf("Converting VHDX to qcow2: %s", bundleDir)

	cmd := exec.Command("qemu-img", "convert",
		"-f", "vhdx", // VHDX v2 format (not "vpc" which is VHD v1)
		"-O", "qcow2",
		vhdxPath,
		qcow2Path,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Clean up partial conversion. Ignore the Remove error — we're already
		// returning a more useful error from Run, and the leftover file will be
		// overwritten on the next attempt.
		_ = os.Remove(qcow2Path)
		return fmt.Errorf("qemu-img convert failed: %w", err)
	}

	log.Printf("Conversion complete: %s", qcow2Path)

	// Optionally remove the VHDX to save space
	if b.debug {
		log.Printf("Keeping VHDX file for debugging")
	} else if err := os.Remove(vhdxPath); err != nil && !os.IsNotExist(err) {
		log.Printf("removing VHDX %s after conversion: %v", vhdxPath, err)
	}

	return nil
}

// DecompressBundle decompresses zstd-compressed bundle files.
func (b *BundleManager) DecompressBundle(bundleDir string) error {
	files := []struct {
		compressed   string
		decompressed string
	}{
		{"vmlinuz.zst", "vmlinuz"},
		{"initrd.zst", "initrd"},
		{"rootfs.vhdx.zst", "rootfs.vhdx"},
	}

	for _, f := range files {
		src := filepath.Join(bundleDir, f.compressed)
		dst := filepath.Join(bundleDir, f.decompressed)

		// Skip if already decompressed
		if _, err := os.Stat(dst); err == nil {
			continue
		}

		// Skip if compressed file doesn't exist
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}

		log.Printf("Decompressing %s", f.compressed)
		cmd := exec.Command("zstd", "-d", src, "-o", dst)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("decompressing %s: %w", f.compressed, err)
		}
	}

	return nil
}

// PrepareBundle decompresses and converts a bundle for use with QEMU.
func (b *BundleManager) PrepareBundle(bundleDir string) error {
	if err := b.DecompressBundle(bundleDir); err != nil {
		return fmt.Errorf("decompressing bundle: %w", err)
	}
	if err := b.ConvertVHDX(bundleDir); err != nil {
		return fmt.Errorf("converting VHDX: %w", err)
	}
	return nil
}
