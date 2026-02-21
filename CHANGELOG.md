# Changelog

All notable changes to claude-cowork-service will be documented in this file.

## Unreleased

### Added
- **CI: manual release dispatch** — `workflow_dispatch` trigger with major/minor/patch selector; auto-computes next semver from latest tag, creates and pushes the tag to trigger the full release pipeline
- **RPM packaging** — `packaging/rpm/build-rpm.sh` + `claude-cowork-service.spec` for Fedora/RHEL; builds in `fedora:40` container during CI, `.rpm` included in GitHub Release assets
- **NixOS packaging** — `flake.nix` + `packaging/nix/package.nix` (`buildGoModule`); `packaging/nix/module.nix` provides `services.claude-cowork.enable` for declarative NixOS config
- **CI: RPM build/test** — Fedora container builds, installs, and verifies the `.rpm` before release
- **CI: Nix build** — Validates `nix build` succeeds in the build job

### Fixed
- Revert env var filtering that stripped `CLAUDE_CODE_OAUTH_TOKEN` from app-provided
  environment, causing "Not logged in" errors in Cowork sessions.

## 1.0.4 — 2026-02-21

### Fixed
- CI: fix release title showing "vv1.0.x" (double "v") — `github.ref_name` already includes the `v` prefix.

## 1.0.3 — 2026-02-19

### Fixed
- Makefile: revert LDFLAGS from `?=` to `:=` — `makepkg` exports C linker flags (`-Wl,-O1 ...`) which broke `go build -ldflags`. Command-line overrides (`make LDFLAGS="..."`) still work.

### Added
- CI: build validation step that simulates `makepkg` LDFLAGS environment to prevent LDFLAGS regression.

## 1.0.2 — 2026-02-19

> Community contribution by [@nayrosk](https://github.com/nayrosk) — [PR #1](https://github.com/patrickjaja/claude-cowork-service/pull/1)

### Changed
- CI: Bump `actions/checkout` to `v6.0.2` (latest)
- CI: Auto-sync PKGBUILD version and checksums from release tag
- PKGBUILD: bump `pkgver` to `v1.0.0` (latest)
- Makefile: stop forcing `-s -w` so packagers can handle stripping/debug symbols.
- Makefile: use overridable variables (`?=`) for GO/GOFLAGS/LDFLAGS/CGO_ENABLED.

### Fixed
- Arch/AUR packaging: avoid implicit `sudo` dependency by using `runuser` in post-upgrade restart logic.
- Executable resolution: use login shell (`bash -lc`) for dynamic binary lookup, fixing paths for npm/cargo/nvm-installed tools.

## 1.0.0 — 2026-02-18

### Changed
- **Stable release** — promoted from experimental to v1.0. Full Cowork sessions tested and working end-to-end.

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
