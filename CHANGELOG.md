# Changelog

All notable changes to claude-cowork-service will be documented in this file.

## 0.3.1 — 2026-02-16

### Fixed
- **Fallback executable resolution for systemd** — when `exec.LookPath` fails (systemd user services have minimal PATH missing `~/.local/bin`), the daemon now checks `~/.local/bin`, `/usr/local/bin`, and `/usr/bin` as fallback candidates. Fixes "claude: executable file not found in $PATH" error for CustomPlugins.

## 0.3.0 — 2026-02-16

### Fixed
- **Bidirectional path remapping on stdin/stdout** — Claude Desktop sends VM-style absolute paths (`/sessions/<name>/mnt/...`) in stdin messages (initialize, workspace folders) after spawn. These were not being rewritten, causing Claude Code to fail resolving paths. Now `writeStdin` replaces `/sessions/<name>` → real session dir (forward), and `streamOutput` replaces real paths → `/sessions/<name>` (reverse) so Desktop's path translator still works.

### Fixed
- **Concurrent event write safety** — `subscribeEvents` callbacks now serialize writes with a mutex and stop sending after the first write failure, preventing interleaved length-prefixed messages on the socket
- **Atomic WriteMessage** — `WriteMessage` now writes length prefix + payload in a single `conn.Write` call to prevent partial writes from concurrent goroutines

## 0.2.0 — 2026-02-11

### Added
- **Universal install script** (`scripts/install.sh`) — one-liner install for any Linux distro, supports `--user` (no root) and `--uninstall`
- **Makefile PREFIX/DESTDIR support** — GNU-convention variables for flexible install paths (`make PREFIX=/usr/local install`)

### Changed
- **README** — multi-distro installation docs (Quick Install, AUR, From Source)

## 0.1.0 — 2026-02-11

Initial release.

### Added
- **Native Linux backend** — executes commands directly on the host via `os/exec`, no VM overhead
- **Full RPC protocol** — 17 method handlers matching Windows `cowork-svc.exe` wire protocol (length-prefixed JSON over Unix socket)
- **Session management** — creates session directories under `~/.local/share/claude-cowork/sessions/` with symlink-based path remapping for VM-compatible paths
- **MCP server interception** — strips `sdkMcpServers` from initialize requests and auto-responds to `mcp_message` control requests to prevent Claude Code from blocking
- **Stream-json relay** — captures Claude Code stderr output (stream-json format) and emits it as stdout events back to Claude Desktop
- **systemd user service** — `claude-cowork.service` with restart-on-failure
- **CI/CD pipeline** — `go vet` + build + test on push; binary release + AUR publish on tag
- **Dormant VM backend** — `vm/` directory contains full QEMU/KVM + vsock implementation for future sandboxed execution

### Protocol discoveries
12 mismatches found during reverse engineering of the Windows protocol — see README.md for the full table.
