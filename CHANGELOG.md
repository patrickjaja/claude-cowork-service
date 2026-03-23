# Changelog

All notable changes to claude-cowork-service will be documented in this file.

## Unreleased

### Added
- **`--brief` flag injection** — Inject `--brief` CLI flag when `CLAUDE_CODE_BRIEF=1` is in env (redundant safety measure for SendUserMessage availability)
- **Dispatch debug logging** — Log `CLAUDE_CODE_BRIEF`, `--tools`, and `--allowedTools` at spawn time for dispatch debugging

### Fixed
- **`present_files` permission denied** — Remove `mcp__cowork__present_files` from `--disallowedTools`; Electron blocks it for dispatch (expects `SendUserMessage` with attachments), but on Linux the model needs it as a fallback for file sharing

## 1.0.14 — 2026-03-23

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
