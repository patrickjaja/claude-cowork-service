# Changelog

All notable changes to claude-cowork-service will be documented in this file.

## Unreleased

### Added
- **KVM backend** (`-backend=kvm`) — new QEMU/KVM-based guest runtime that replaces the old dormant VM implementation. Selectable via the `-backend` flag or the `COWORK_VM_BACKEND` environment variable. Listens on a dedicated socket (`cowork-kvm-service.sock`) so native and KVM daemons can coexist in the same `$XDG_RUNTIME_DIR`. Native remains the default.
  - `vm/backend.go` — session lifecycle, bundle preparation, memory/CPU configuration, process management
  - `vm/bridge.go` — vsock host↔guest JSON message bridge
  - `vm/qemu.go` — QEMU launch spec, root-disk boot (no more throwaway overlay), virtiofs `$HOME` share
  - `vm/qmp.go` — QMP control channel for live networking and shutdown
  - `vm/vfs.go` + `vm/helper.go` — VFS helper runs inside `unshare --user --map-root-user --mount` (invoked via `--vfs-helper` re-exec) to set up mounts without root on the host
  - `vm/preflight.go` — `CheckKvmPrerequisites()` gates startup on `/dev/kvm`, `qemu-system-x86_64`, and vhost-vsock
- **VHDX → qcow2 conversion caching** — root disk is converted once and reused across reboots. Base-image updates are detected via a trailer canary instead of a full SHA-256 scan, eliminating multi-second startup hashing.
- **Shared session disk** — session state persists across all sessions of a given host instead of a per-host disk, matching upstream behavior.
- **Log line truncation** — new `logx` package centralizes log output. Long JSON payloads (RPC params, `EVENT → client`, guest messages, `writeStdin` bodies, MCP-PROXY frames) are now truncated to 160 characters by default with a `…(+N more)` suffix showing how many characters were dropped. Previously these lines ran for thousands of characters or were truncated inconsistently at 200/300/500/2000/5000 characters by two near-duplicate helpers.
- **`-log-full-lines` flag** — disables truncation globally for the session when you actually need the full payload. Also accepts `COWORK_LOG_FULL=1` environment variable as a fallback.
- **`-log-max-len` flag** — override the default 160-character budget.

### Changed
- **Upstream update to Claude Desktop v1.3109.0** (from v1.3036.0)
- **cowork-svc.exe**: Clean rebuild, **byte-identical size** (12,648,272 bytes), same Go version (go1.24.13). Only build metadata changed (VCS revision `35cbf6530e05912137624cde0f075dc7f121fa60`, build timestamp `2026-04-16T20:32:01Z`). No new handler functions, no new error strings.
- **Default socket path depends on backend** — native keeps the historical `cowork-vm-service.sock` for Desktop compatibility; KVM uses `cowork-kvm-service.sock` so Desktop can tell the two modes apart by which socket exists.
- **Logging call sites consolidated** — `pipe/handlers.go` (RPC dispatch, `handleWriteStdin`, `handleSpawn`, `handleSubscribeEvents`), `vm/bridge.go`, `vm/backend.go`, and `native/process.go` now route through `logx.Debug` / `logx.Info`, removing scattered `if h.debug { log.Printf(...) }` wrappers. The `setDebugLogging` RPC still toggles debug output at runtime.
- **Retired duplicate truncation helpers** — `vm/bridge.go#truncate` and `native/process.go#truncateLine` are gone; all call sites use `logx.Trunc`.
- **MCP-PROXY detection logs are now gated** — `[native] >>>MCP-PROXY>>>`, `<<<MCP-PROXY<<<`, and `<<<MCP-INIT<<<` lines emit only under `-debug`, matching the documented "quiet by default" behavior.
- **Upstream update to Claude Desktop v1.3036.0** (from v1.2773.0)
- **cowork-svc.exe**: Minor rebuild (+4,096 bytes, 12,644,176 → 12,648,272 bytes), same Go version (go1.24.13). No new RPC handler functions. New Windows-only certificate store helpers: `vm.enumerateCertStore`, `vm.certChainsToTrustedRoot` (backed by `windows.CertGetCertificateChain` / `CertFreeCertificateChain`). New error string `"[VM] Failed to load host CA certificates: %v"`.
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **app.asar**: Grew significantly (10.1 → 14.6 MB extracted JS) but contents are overwhelmingly minifier symbol renames. All 22 of our RPC methods are still referenced, and session dispatch logic (`CLAUDE_CODE_TAGS`, `CLAUDE_CODE_BRIEF`, `disallowedTools`, `present_files`, `session_type:"cowork"`) is unchanged.
- **SDK versions unchanged** — Electron 41.2.0, claude-agent-sdk 0.2.92, claude-agent-sdk-future 0.2.93-dev, @modelcontextprotocol/sdk 1.28.0
- **No Go code changes needed** — all 22 RPC methods, 8 event types, spawn parameters, and wire format are identical
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.3109.0
- **Prior upstream update to Claude Desktop v1.3036.0** (from v1.2773.0, commit `95c768f`)
- **cowork-svc.exe** (v1.3036.0): Minor rebuild (+4,096 bytes, 12,644,176 → 12,648,272 bytes), same Go version (go1.24.13). No new RPC handler functions. New Windows-only certificate store helpers: `vm.enumerateCertStore`, `vm.certChainsToTrustedRoot` (backed by `windows.CertGetCertificateChain` / `CertFreeCertificateChain`). New error string `"[VM] Failed to load host CA certificates: %v"`.
- **New Desktop-side features** (no pipe protocol impact):
  - **`ENABLE_PROMPT_CACHING_1H=1`** — new environment variable injected by Desktop into every spawned Claude Code process (alongside `CLAUDE_CODE_IS_COWORK=1`, `DISABLE_MICROCOMPACT=1`). Our backend passes env through transparently — no handler change required.
  - **`cowork-plugin-oauth` storage** — new `[PluginOAuthStorage]` local `conf` file for per-plugin OAuth credentials
  - **CoworkArtifacts lifecycle** — new `[CoworkArtifacts] Created` / `Imported` log lines; new telemetry `cowork_artifacts_created`, `cowork_artifacts_imported` (in addition to existing `Updated`/`Exported`)
  - **New telemetry events** — `cu_lock_released`, `cu_teach_session`, `lam_mcp_servers_setup_summary`
  - **`cowork_lock_midsession_model`** — new gate preventing mid-session model changes in cowork sessions
  - **Feature gate `3444158716`** — new gate keyed on `sessionType==="cowork"` (purpose not fully identifiable from minified source)
  - **GrowthBook gates** — new top-level `louderPenguin`, `operon` added alongside existing `coworkKappa` (non-cowork features)
  - **`setup-cowork` skill** — new built-in skill command driven by feature flag `skillPrompt`
  - IPC UUID changed (`f189fbc9...` → `08aa66e6-e7d3-4eb8-95ac-7e3f613ce196`) — rebuild artifact, no protocol impact
- **Prior upstream update to Claude Desktop v1.2773.0** (from v1.2581.0, commit `c17612d`): minor cowork-svc.exe rebuild (+512 bytes), SDK rolled back to 0.2.92, Desktop-side `[cowork-deletion]` event logging, `dispatchOnCliOpAlwaysAllowed`, `coworkWebSearchEnabled` gate removed.

### Removed
- **Legacy VM implementation** — `vm/manager.go`, `vm/network.go`, `vm/vsock.go`, and `process/spawn.go` deleted. The new `vm/backend.go` + `vm/bridge.go` pair subsumes their roles (lifecycle, networking, vsock, process tracking) with a cleaner architecture built around QEMU/KVM and QMP.
- **Root overlay boot mode** — the guest now boots directly off the converted root disk, so filesystem changes persist across reboots instead of being thrown away on every startup.

## 1.0.49 — 2026-04-14

## 1.0.48 — 2026-04-14

### Fixed
- **APT install script hardcoded `arch=amd64`** — `packaging/apt/install.sh` and `packaging/apt/index.html` now detect architecture via `dpkg --print-architecture`, so ARM64 users (Raspberry Pi 5, Jetson, DGX Spark) get the correct `arm64` repo entry instead of a broken `amd64` one

### Added
- **Nix aarch64-linux support** — `flake.nix` (`supportedSystems`) and `packaging/nix/package.nix` (`meta.platforms`) now include `aarch64-linux`
- **Raspberry Pi 5** added to supported ARM64 devices in README

## 1.0.47 — 2026-04-14

## 1.0.46 — 2026-04-14

### Changed
- **Upstream update to Claude Desktop v1.2581.0** (from v1.2278.0)
- **cowork-svc.exe**: Clean rebuild, same size (12,643,664 bytes), same Go version (go1.24.13). No new RPC handler functions. Only build metadata changed (commit hash, timestamps)
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **New Desktop-side features** (no pipe protocol impact):
  - `cowork-file` URL scheme — custom Electron protocol for native file preview within Desktop
  - `coworkNativeFilePreview` feature flag — enables WebContentsView for previewing `.docx`, `.pptx`, `.pdf`, `.svg`, `.htm` files
  - Office file conversion via LibreOffice (`soffice --headless --convert-to pdf`) inside VM
  - `coworkKappa` feature flag — new Desktop-side gate
  - `getCodeStats` IPC method — Electron-side code statistics (not a pipe RPC)
  - Permission routing refactored — `handlePermissionResponse` now distinguishes `cowork` vs `ccd` products
  - `getPermissionSessionRoute` method — routes permission requests to correct session type
  - `cowork-plugin-shim.sh` updated — token gating (`cowork_require_token`), permission confirmation protocol (`cowork_gate`), multi-arch binary dispatch (`cowork_exec`)

### Fixed
- **`apiReachability` event JSON field** — Desktop reads `s.status` but our struct used `json:"reachability"`. Changed to `json:"status"` to match actual wire protocol
- **`startupStep` event missing `status` field** — Desktop guards with `s.step && s.status` and distinguishes `"started"` vs `"completed"`. Added `Status` field and emit both phases for each step

### Added
- **`networkStatus` event type** — Desktop handles `case "networkStatus"` events with `"CONNECTED"` / `"NOT_CONNECTED"` status. Emitted as `"CONNECTED"` during native startup since host has direct network access
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md` (9 event types), `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.2581.0

## 1.0.45 — 2026-04-08

### Changed
- **Upstream update to Claude Desktop v1.1617.0** (from v1.1348.0)
- **cowork-svc.exe**: Rebuild with minor size increase (+1.5 KB, 11,177,808 → 11,179,344 bytes), same Go version (go1.24.13), no new RPC methods or handler functions. TLS certificate date rotation, updated build timestamps and VCS revision
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **SDK versions unchanged** — Electron 40.8.5, claude-agent-sdk 0.2.92, claude-agent-sdk-future 0.2.93-dev, conway-client unchanged
- **No Go code changes needed** — all 22 RPC methods, 8 event types, spawn parameters, and wire format are identical
- **New Desktop-side features** (no pipe protocol impact):
  - `coworkEgressAllowedHosts` admin setting — enterprise/MDM-configurable egress allowlist, merges into existing `allowedDomains` spawn param (Desktop resolves before RPC)
  - `canUseTool` VM path guard — blocks host-loop tools from operating on `/sessions/` paths
  - `cowork-plugin-shim.sh` integration — Desktop now actively copies shim script into `.cowork-lib` mount during session setup
  - `request_cowork_directory` storage guard — prevents mounting session's own internal storage directory
  - `_syncPlugins` timeout — plugin sync now has 5-second timeout for account resolution
  - `getSessionStorageDir` replaces `mountFolder` — internal Desktop refactor (no RPC change)
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.1617.0

## 1.0.44 — 2026-04-07

## 1.0.43 — 2026-04-05

## 1.0.42 — 2026-04-03

### Fixed
- **Double-nested home directory in symlink targets** — Claude Desktop v1.569.0+ changed `getVMStorageSubpath` to return root-relative subpaths (`home/user/.config/...`), causing `filepath.Join(home, relPath)` to produce doubled paths (`/home/user/home/user/.config/...`). Added `resolveSubpath()` helper that detects the format and resolves correctly. Fixes #16.

### Changed
- **Upstream update to Claude Desktop v1.1062.0** (from v1.569.0)
- **cowork-svc.exe**: Internal cert handling refactored (`enumerateRootStore`), new `vm/rpc_types.go` source file; binary shrank 8KB (11,186,000 → 11,177,808 bytes); same Go version (go1.24.13); no new RPC methods
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **SDK versions**: claude-agent-sdk 0.2.92 (was 0.2.87), claude-agent-sdk-future 0.2.93-dev (was 0.2.90-dev), conway-client updated; Electron unchanged at 40.8.5
- **No Go code changes needed** — all 22 RPC methods, 8 event types, spawn parameters, and wire format are identical
- **New Desktop features** (all Electron app-layer, no pipe protocol impact):
  - Cowork onboarding system (`cowork-onboarding` MCP server, `setup-cowork` skill)
  - Cowork search subsystem (`searchSessions` IPC)
  - Session file operations (`readFileAtCwd`, `writeSessionFile`, etc.)
  - Deploy/preview system (`deployPreview`, `suggestDeployName`, `unpublishDeploy`)
  - Marketplace enhancements (`createAccountMarketplace`, `uploadAccountPlugin`, etc.)
  - Connectors concept (`suggest_connectors` MCP tool)
  - Transcript feedback, auto-fix toggle, cowork egress blocking
  - Expanded disallowed tools lists (onboarding tool, CU-only restrictions)
  - Removed settings: `isClaudeCodeForDesktopEnabled`, `isDesktopExtensionEnabled`, `autoUpdaterEnforcementHours`, `setCiMonitorEnabled`, `forceLoginOrgUUID`, `customDeploymentUrl`
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.1062.0

## 1.0.41 — 2026-04-03

## 1.0.40 — 2026-04-02

### Fixed
- **Dispatch file delivery**: Inject `--append-system-prompt` for dispatch sessions instructing the model to use `attachments` on `SendUserMessage` instead of `computer://` links that don't reach remote/mobile users
- **present_files hint restored**: Re-add model hint in `present_files` response telling it to also call `SendUserMessage` with `attachments` (removed in df8037e when fixing INVALID_PATH)

### Changed
- **Upstream update to Claude Desktop v1.569.0** (from v1.2.234)
- **cowork-svc.exe**: New `handleSendGuestResponse` handler function; binary grew ~11KB (11,174,736 → 11,186,000 bytes); same Go version (go1.24.13)
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **SDK versions unchanged** — Electron 40.8.5, claude-agent-sdk 0.2.87, MCP SDK 1.28.0
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.569.0

### Added
- **New RPC method `sendGuestResponse`** — Handler for plugin permission bridge guest responses; no-op on native Linux (filesystem-based permission bridge handles this directly)
- Protocol now at **22 RPC methods**, 8 event types
- **README: Systemd service documentation** — Documents `ExecStartPre` environment import, all 8 Wayland/display env vars, and why they're needed
- **README: Dependencies table** — Runtime (systemd, bash), functional (Claude Code CLI), optional (socat), and build-time (Go 1.21+) deps
- **README: Troubleshooting section** — Covers Wayland display issues, ydotool/Computer Use, and `claude` binary resolution
- **README: Claude Code dependency clarification** — Explains why it's `optdepends` (users need latest version) with install methods (npm, AUR, Nix)
- **NixOS module: Wayland environment import** — `ExecStartPre` in `module.nix` matching the standard service file, using NixOS-correct paths (`${pkgs.bash}`, `${pkgs.systemd}`)

## 1.0.39 — 2026-04-01

### Changed
- **Upstream update to Claude Desktop v1.2.234** (from v1.1.9669)
- **cowork-svc.exe**: Rebuild only — same size (11,174,736 bytes), same Go version (go1.24.13), no new RPC methods or handler functions. Updated build timestamps and VCS revision
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **Electron 40.8.5** (was 40.4.1), **claude-agent-sdk-future 0.2.90-dev** (was 0.2.86-dev)
- **New Desktop features** (no Go code changes needed):
  - `dispatchCodeTasksPermissionMode` preference for dispatch code task permission modes
  - `start_code_task` MCP dispatch tool for code-specific work
  - Plugin permission bridge mounts (`.cowork-perm-req`, `.cowork-perm-resp`) in `additionalMounts` — handled by existing mount symlink logic
  - `.cowork-lib` plugin shim library mount — handled by existing mount symlink logic
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.2.234

## 1.0.38 — 2026-04-01

## 1.0.37 — 2026-03-31

### Fixed
- **claude-cowork.service**: Import Wayland/display environment variables (`WAYLAND_DISPLAY`, `XDG_SESSION_TYPE`, `XDG_CURRENT_DESKTOP`, `DISPLAY`, `DBUS_SESSION_BUS_ADDRESS`, `HYPRLAND_INSTANCE_SIGNATURE`, `SWAYSOCK`) via `ExecStartPre` so spawned CLI processes can access display and D-Bus services. Critical on Wayland-only systems (e.g. Ubuntu 25.10+). Fixes [#13](https://github.com/patrickjaja/claude-cowork-service/issues/13).

## 1.0.36 — 2026-03-31

## 1.0.35 — 2026-03-31

## 1.0.34 — 2026-03-31

## 1.0.33 — 2026-03-31

## 1.0.32 — 2026-03-31

## 1.0.31 — 2026-03-31

## 1.0.30 — 2026-03-31

## 1.0.29 — 2026-03-31

## 1.0.28 — 2026-03-31

## 1.0.27 — 2026-03-31

## 1.0.26 — 2026-03-30

### Added
- **Upstream update to Claude Desktop v1.1.9669** (from v1.1.9493)
- **3 new RPC handlers**: `getSessionsDiskInfo`, `deleteSessionDirs`, `createDiskImage` — all no-ops on native Linux (no virtual disks needed). Desktop's `VMDiskJanitor` and conda integration call these methods
- **5 new spawn parameters**: `isResume`, `allowedDomains`, `oneShot`, `mountSkeletonHome`, `mountConda` — parsed from JSON but ignored on native (no VM/network isolation)
- **Protocol now handles 21 RPC methods** (up from 18)

### Changed
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.1.9669 with new checksums, methods, and version history
- **VM bundle SHA**: `5680b11bcdab215cccf07e0c0bd1bd9213b0c25d` (all file checksums changed)
- **New upstream file**: `cowork-plugin-shim.sh` — plugin permission gating library (filesystem-based request/response protocol)
- **New asar file**: `coworkArtifact.js` — Electron preload exposing `window.cowork.callMcpTool()` for web artifacts to invoke MCP tools

## 1.0.25 — 2026-03-29

## 1.0.24 — 2026-03-29

### Fixed
- **`present_files` INVALID_PATH error** — Return individual file paths as content items instead of a descriptive text message. Desktop's renderer treats each `{type:"text", text:...}` entry as a file path and calls `readLocalFile` on it; our previous response ("Files verified on disk (1). NOTE: ...") was passed verbatim as a path, causing `[INVALID_PATH] Path must be absolute`

## 1.0.23 — 2026-03-29

### Added
- **Upstream protocol documentation** — Added comprehensive reference docs reverse-engineered from Claude Desktop v1.1.9493: `COWORK_RPC_PROTOCOL.md` (all 18 RPC methods, 8 event types, 12 protocol discoveries, Linux-specific adaptations, session lifecycle), `COWORK_SVC_BINARY.md` (Go binary internals, app.asar SDK versions, checksums), and `COWORK_VM_BUNDLE.md` (VM rootfs deep dive: sdk-daemon, Node.js, Python/npm packages, system packages)
- **Automated version-check CI** — New `.github/workflows/version-check.yml` workflow polls `downloads.claude.ai` every 2 hours, creates a GitHub issue when a new Claude Desktop version is detected, and updates version badges on gh-pages (Claude Desktop tracking, APT, RPM, Nix)
- **Version update playbook** — Added `update-prompt.md` with reusable prompts for the full update workflow (extract, diff protocol changes, audit Go code compatibility) and `UPDATE-PROMPT-CC-INPUT-MANUAL.md` as a quick-reference entry point
- **Project guidelines** — Added `CLAUDE.md` with build/run instructions, key file purposes, deep analysis workflow, debugging commands, and architecture notes
- **README badges and docs section** — Added 6 status badges (Claude Desktop version, AUR, APT repo, RPM repo, Nix flake, CI) and an "Upstream Reference Docs" section linking to the three new protocol/binary/bundle documents
- **`.upstream-version` tracking file** — Committed file tracking the upstream Claude Desktop version for CI; fixes version-check workflow which previously read `bin/.version` (gitignored, never available in CI)
- **`.vm-analysis/` in `.gitignore`** — Scratch directory used during deep analysis of upstream binaries

## 1.0.22 — 2026-03-28

## 1.0.21 — 2026-03-27

### Added
- **Dispatch: full native Linux support** — Dispatch now works end-to-end on Linux. The Ditto orchestrator agent calls `SendUserMessage` natively (CLI v2.1.86 fix), text responses render on phone, and file delivery uses attachment hints
- **Strip `--disallowedTools`** — Desktop passes VM-only tool restrictions (`AskUserQuestion`, `mcp__cowork__present_files`, `mcp__cowork__allow_cowork_file_delete`, `mcp__cowork__launch_code_session`, `mcp__cowork__create_artifact`, `mcp__cowork__update_artifact`). On native Linux we strip the entire flag since there is no VM runtime
- **Local `present_files` interception** — Intercept `mcp__cowork__present_files` MCP control_requests in `streamOutput`, verify files exist on disk, and return synthetic success response. Desktop's handler rejects native Linux paths; this bypasses it entirely. Response includes hint to use `SendUserMessage` with `attachments` for mobile delivery
- **Reverse mount path mapping** — Build reverse mount remaps (real host path → VM `/sessions/<name>/mnt/<mount>`) applied to outgoing MCP control_requests. Ensures Desktop's MCP protocol can resolve paths for tools other than `present_files`
- **Dispatch architecture documentation** — Added [Dispatch Support](README.md#dispatch-support) section to README documenting Ditto agent, session types, all Linux adaptations, `SendUserMessage` signature, and debugging commands
- **NixOS module evaluation tests in CI** — Verify module.nix produces correct systemd service config (ExecStart, Restart, wantedBy, extraPath wiring) via `nix flake check`

### Changed
- **`--brief` injection is now conditional** — Only inject `--brief` when Desktop passes `CLAUDE_CODE_BRIEF=1` (for Ditto/dispatch sessions), not for regular cowork sessions. Desktop correctly differentiates: `lam_session_type:agent` gets BRIEF=1, `lam_session_type:chat` does not

### Removed
- **Hardcoded binary fallback paths** — Removed Stage 4 fallback (`~/.npm-global/bin`, `~/.local/bin`, etc.) from binary resolution; stages 1-3 (LookPath, login shell, interactive shell) already resolve user-installed binaries reliably, and NixOS users now have `extraPath`

## 1.0.16 — 2026-03-23

### Changed
- **SDK MCP proxy — pass `--mcp-config` through unchanged** — Stopped stripping SDK MCP servers from `--mcp-config`. Claude Desktop's session manager handles the bidirectional `control_request`/`control_response` MCP proxy over the event stream, identical to VM mode on Mac/Windows. This enables all per-session SDK tools: `mcp__dispatch__send_message`, `mcp__dispatch__start_task`, `mcp__cowork__present_files`, `mcp__session_info__read_transcript`, and more. Verified with 161 control_request/response pairs in test run, zero blocking.
- **Removed `present_files` disallowedTools workaround** — No longer needed since the SDK MCP proxy gives the model native access to all cowork tools
- **MCP proxy debug logging** — Detect and log `control_request` (CLI→Desktop) and `control_response` (Desktop→CLI) messages in stdout/stdin streams for observability

### Added
- **`--brief` flag injection** — Inject `--brief` CLI flag when `CLAUDE_CODE_BRIEF=1` is in env (redundant safety measure for SendUserMessage availability)
- **Dispatch debug logging** — Log `CLAUDE_CODE_BRIEF`, `--tools`, and `--allowedTools` at spawn time for dispatch debugging

## 1.0.15 — 2026-03-23

### Fixed
- **ELOOP self-referencing symlink** — Prevent `.mcpb-cache` (and other child mounts) from becoming self-referencing symlinks when a parent mount is already symlinked; fixes `ELOOP: too many symbolic links encountered` on Dispatch/Cowork sessions with remote plugins
- **Premature SIGTERM on Dispatch results** — Add 1s delay in kill RPC handler before sending SIGTERM, giving the result event time to propagate to the Electron renderer; fixes Dispatch responses completing successfully but never appearing in the UI

## 1.0.13 — 2026-03-20

## 1.0.12 — 2026-03-20

### Fixed
- **Response ID propagation** — Echo back request `id` in all RPC responses so claude-desktop-bin's vm-client can match them; fixes "Orphaned response id=0 method response dropped" errors with claude-desktop-bin >= 1.1.7714
- **`isGuestConnected` always true** — On native Linux the host IS the guest, so return `true` unconditionally; fixes "Request timed out: isGuestConnected" when claude-desktop-bin calls this before `startVM`
- **Skip non-directory mounts** — Filter out mounts targeting files (e.g. `app.asar`) instead of symlinking them into the session; fixes "is not a directory" CLI error when Claude Desktop passes file mounts as `--add-dir`

## 1.0.11 — 2026-03-19

### Added
- **`isDebugLoggingEnabled` RPC** — Returns current debug logging state (matches Windows cowork-svc.exe protocol)
- **`startupStep` events** — Emits `CERTIFICATE` and `VirtualDiskAttachments` startup progress events during `startVM` (matches Windows cowork-svc.exe protocol)

## 1.0.10 — 2026-03-04

### Fixed
- **Binary PATH resolution** — Add interactive login shell fallback (`$SHELL -lic`) to resolve binaries when `bash -lc` misses PATH entries set in `.bashrc` behind interactive guards; also add `~/.npm-global/bin` to hardcoded fallback paths; fixes "Failed to sample" error in Cowork sessions when `claude` is installed via npm global
