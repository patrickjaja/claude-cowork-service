package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/patrickjaja/claude-cowork-service/logx"
	"github.com/patrickjaja/claude-cowork-service/native"
	"github.com/patrickjaja/claude-cowork-service/pipe"
	"github.com/patrickjaja/claude-cowork-service/vm"
)

var version = "dev"

// pipe.VMBackend plus a Shutdown hook for exit cleanup.
type backendWithShutdown interface {
	pipe.VMBackend
	Shutdown()
}

func main() {
	// Re-exec path: running in VFS helper mode (inside unshare --user
	// --map-root-user --mount).
	if len(os.Args) > 1 && os.Args[1] == "--vfs-helper" {
		os.Exit(vm.RunVfsHelper(os.Args[2:]))
	}

	socketPath := flag.String("socket", "", "Unix socket path (default depends on backend)")
	debug := flag.Bool("debug", false, "Enable debug logging")
	backendName := flag.String("backend", defaultBackend(), "Backend: native or kvm")
	bundlesDir := flag.String("bundles-dir", defaultBundlesDir(), "VM bundles directory (kvm backend only)")
	showVersion := flag.Bool("version", false, "Show version and exit")
	logFullLines := flag.Bool("log-full-lines", false, "Don't truncate long log lines (JSON payloads, RPC params, events)")
	logMaxLen := flag.Int("log-max-len", 160, "Max characters per log line before truncation (ignored with -log-full-lines)")
	flag.Parse()

	if *socketPath == "" {
		*socketPath = defaultSocketPath(*backendName)
	}

	if *showVersion {
		fmt.Printf("cowork-svc-linux %s\n", version)
		os.Exit(0)
	}

	fullLines := *logFullLines || os.Getenv("COWORK_LOG_FULL") == "1"
	logx.Configure(*debug, fullLines, *logMaxLen)

	log.Printf("cowork-svc-linux %s starting (%s backend)", version, *backendName)
	log.Printf("Socket: %s", *socketPath)

	var backend backendWithShutdown
	switch *backendName {
	case "native":
		backend = native.NewBackend(*debug)
	case "kvm":
		check := vm.CheckKvmPrerequisites()
		if !check.OK {
			log.Fatalf("KVM backend unavailable: %s", check.Reason)
		}
		backend = vm.NewKvmBackend(*bundlesDir, *debug)
		log.Printf("Bundles dir: %s", *bundlesDir)
	default:
		log.Fatalf("Unknown backend %q (expected native or kvm)", *backendName)
	}

	server := pipe.NewServer(*socketPath, backend, *debug)
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	log.Printf("Listening on %s", *socketPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %s, shutting down...", sig)
	backend.Shutdown()
}

// defaultSocketPath picks the socket name from the backend so Claude Desktop
// can tell which mode the daemon is running just by looking at what sockets
// exist in $XDG_RUNTIME_DIR. Native keeps the historical name for
// compatibility with the Windows `cowork-vm-service` client.
func defaultSocketPath(backend string) string {
	name := "cowork-vm-service.sock"
	if backend == "kvm" {
		name = "cowork-kvm-service.sock"
	}
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, name)
	}
	return filepath.Join("/tmp", name)
}

// defaultBackend honors COWORK_VM_BACKEND env var (matches the JS service),
// falls back to "native" to preserve existing behavior.
func defaultBackend() string {
	if v := os.Getenv("COWORK_VM_BACKEND"); v != "" {
		return v
	}
	return "native"
}

func defaultBundlesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "Claude", "vm_bundles")
}
