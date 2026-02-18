# Changelog

All notable changes to claude-cowork-service will be documented in this file.

## 0.3.3 — 2026-02-18

### Added
- **APT repository for Debian/Ubuntu** — CI now builds `.deb` packages and deploys a GPG-signed APT repository to GitHub Pages. Users can install via `curl -fsSL .../install.sh | sudo bash && sudo apt install claude-cowork-service`.
- **Debian package build script** (`packaging/debian/build-deb.sh`) — creates `.deb` from the static Go binary + systemd user service
- **APT repo tooling** (`packaging/apt/`) — install script, update-apt-repo script, and GitHub Pages landing page

## 0.3.2 — 2026-02-16

### Fixed
- **MCP server proxying (Browse, Web Search)** — Removed three interception points that blocked MCP traffic between Claude Code and Claude Desktop. Browse tool modals now stream content correctly in Cowork sessions.
  - Removed `stripSdkMcpServers` from `WriteStdin` — Desktop's `sdkMcpServers` list now reaches Claude Code so it discovers Browse/Web Search tools
  - Removed `handleMcpRequest` interception from `streamOutput` — MCP messages flow through to Desktop for proxying instead of being auto-errored
  - `--mcp-config` stripping in `Spawn()` is intentionally kept (prevents direct stdio MCP connections)

### Added
- **Pacman post-upgrade hook** — `claude-cowork-service.install` automatically restarts the systemd user service after package upgrades via `yay`/`pacman`

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
- **MCP message pass-through** — forwards MCP traffic between Claude Code and Claude Desktop for parent-proxied MCP servers (Browse, Web Search)
- **Stream-json relay** — captures Claude Code stderr output (stream-json format) and emits it as stdout events back to Claude Desktop
- **systemd user service** — `claude-cowork.service` with restart-on-failure
- **CI/CD pipeline** — `go vet` + build + test on push; binary release + AUR publish on tag
- **Dormant VM backend** — `vm/` directory contains full QEMU/KVM + vsock implementation for future sandboxed execution

### Protocol discoveries
12 mismatches found during reverse engineering of the Windows protocol — see README.md for the full table.
