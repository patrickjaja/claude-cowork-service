# Changelog

All notable changes to claude-cowork-service will be documented in this file.

## Unreleased

### Added
- **Upstream protocol documentation** — Added comprehensive reference docs reverse-engineered from Claude Desktop v1.1.9493: `COWORK_RPC_PROTOCOL.md` (all 18 RPC methods, 8 event types, 12 protocol discoveries, Linux-specific adaptations, session lifecycle), `COWORK_SVC_BINARY.md` (Go binary internals, app.asar SDK versions, checksums), and `COWORK_VM_BUNDLE.md` (VM rootfs deep dive: sdk-daemon, Node.js, Python/npm packages, system packages)
- **Automated version-check CI** — New `.github/workflows/version-check.yml` workflow polls `downloads.claude.ai` every 2 hours, creates a GitHub issue when a new Claude Desktop version is detected, and updates version badges on gh-pages (Claude Desktop tracking, APT, RPM, Nix)
- **Version update playbook** — Added `update-prompt.md` with reusable prompts for the full update workflow (extract, diff protocol changes, audit Go code compatibility) and `UPDATE-PROMPT-CC-INPUT-MANUAL.md` as a quick-reference entry point
- **Project guidelines** — Added `CLAUDE.md` with build/run instructions, key file purposes, deep analysis workflow, debugging commands, and architecture notes
- **README badges and docs section** — Added 6 status badges (Claude Desktop version, AUR, APT repo, RPM repo, Nix flake, CI) and an "Upstream Reference Docs" section linking to the three new protocol/binary/bundle documents
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
