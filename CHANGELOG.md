# Changelog

All notable changes to claude-cowork-service will be documented in this file.

## 1.2.0 — 2026-07-01 - Final release (goodbye)

### Changed
- **Final "goodbye" release.** This is the last version that will be published to the AUR. The package remains owned (not orphaned) to prevent a takeover, but receives no further updates.
- **PKGBUILD: removed the `claude-desktop-bin` OptDepends.** Now that `claude-desktop-bin` repackages the official Claude Desktop Linux `.deb` (which runs Cowork via its own native VM backend), it no longer uses this daemon. Dropping the cross-link also removes the stale `Required by: claude-cowork-service` entry from the `claude-desktop-bin` AUR page.
- **PKGBUILD: `pkgdesc` now prefixed `[DEPRECATED - UNMAINTAINED]`** so the status is visible in `yay -Ss` and on the AUR page.
- **Install hook: prints a deprecation notice** on install/upgrade, pointing users to the official Claude Desktop Linux build and to `claude-desktop-bin`.

## 2026-06-30 - DEPRECATED & UNMAINTAINED

### Deprecated
- **This project is deprecated and no longer maintained; the repository will be archived.** On 2026-06-30 Anthropic shipped an official Claude Desktop Linux beta (`.deb` + apt repo, `downloads.claude.ai/claude-desktop`; docs at https://code.claude.com/docs/en/desktop-linux). The official build ships its own native Cowork VM backend - `cowork-linux-helper` (a Go binary, module `coworkd`), `virtiofsd`, a `smol-bin.x64.img` guest image, and QEMU/OVMF - speaking essentially the same length-prefixed JSON-over-vsock RPC protocol this daemon reverse-engineered. It supersedes this daemon's KVM mode entirely, and the sibling project `claude-desktop-bin` has been refactored to repackage the official `.deb` and no longer depends on this daemon.
- **Known limitation (why someone might still want this):** the official Cowork hard-requires `/dev/kvm` + `vhost_vsock` with no software-emulation fallback - even its "host-native" mode still boots the VM to run shell commands. This daemon's `native` mode (no VM, no KVM) was the only way to run Cowork on KVM-less hosts (cloud VMs with nested virtualization off, containers, locked-down machines). That niche is no longer served by any actively-maintained project; fork or pin the last release if you need it.
- The codebase, reverse-engineering notes (`COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md`), and the `native`-mode implementation are intentionally retained as a historical/forkable reference. No code is removed by this change.

## 1.1.0 — 2026-06-30

### Added
- **KVM mode now boots the native Linux VM image (`rootfs.img`) via UEFI.** Claude Desktop **v1.17282** introduced a native Linux ("unix") VM bundle that ships a self-booting `rootfs.img` (a GPT disk with its own ESP/GRUB) instead of the Windows `rootfs.vhdx`. The daemon now detects `rootfs.img` and boots it with OVMF UEFI firmware (no external `-kernel`/`-initrd`), attaching it via a copy-on-write qcow2 overlay so the downloaded image stays pristine while system-state edits persist across stop/start. The legacy `rootfs.vhdx` direct-kernel path is kept as a fallback. OVMF firmware is auto-detected across Arch/Debian/Ubuntu/Fedora/RHEL; override with `COWORK_OVMF_CODE` / `COWORK_OVMF_VARS`. UEFI boot is x86_64 only - on aarch64, KVM mode now fails with a clear message directing users to the native backend (which has no such limitation).

### Fixed
- **KVM mode breaks on Claude Desktop v1.17282+ until this version is installed and the service restarted.** v1.17282+ downloads the new `rootfs.img`, which older daemons (which only recognize `rootfs.qcow2`/`rootfs.vhdx`) cannot boot - KVM sessions fail with `no VM bundle available: ... has no rootfs.qcow2 or rootfs.vhdx`. Existing users with a leftover `rootfs.vhdx` in `vm_bundles/` keep working on that until the workspace is cleared/reinstalled; fresh KVM installs break immediately. **Fix: update claude-cowork-service, then restart the service** (`systemctl --user restart claude-cowork`, or kill + relaunch) so Desktop connects to the new binary. The native backend is unaffected.

## 1.0.62 — 2026-06-18

### Fixed
- **Stop shipping a `claude-cowork-service-debug` package**: with the Arch-default `debug` build option enabled in `makepkg.conf`, `makepkg` was stripping the Go binary's symbols into a separate auto-generated `claude-cowork-service-debug` package and pulling it into installs as a dependency. Detached debug symbols aren't useful for this service. Added `options=(!debug)` to the PKGBUILD so no debug package is produced regardless of the builder's `makepkg.conf`; the installed binary is still stripped by makepkg's `strip` option, so nothing else changes.

## 1.0.61 — 2026-06-18

### Fixed
- **Cowork "Not logged in / setting up workspace" loop (native backend, 1p subscription auth)**: a recent Claude Desktop release moved the Cowork OAuth handoff to the `addApprovedOauthToken` RPC + spawn-param and now *strips* `CLAUDE_CODE_OAUTH_TOKEN` from the spawned CLI's environment before the spawn call - on macOS a native addon re-injects it via a MITM proxy. Native Linux has no such proxy and never re-injected the token, so the spawned `claude` CLI started with `apiKeySource=none` and every Cowork turn failed instantly with "Not logged in - Please run /login", leaving the workspace-setup screen stuck. `Spawn` now threads the spawn RPC's own `oauthToken` param into the child env as `CLAUDE_CODE_OAUTH_TOKEN`. 1p-only guard: injection is skipped when the session already carries a credential (`CLAUDE_CODE_OAUTH_TOKEN`/`ANTHROPIC_AUTH_TOKEN`) or is a 3p/gateway setup (`ANTHROPIC_API_KEY` or a Bedrock/Vertex/Foundry base URL), so enterprise auth is never clobbered. KVM mode is unchanged (it forwards the token to the guest via the bridge). No wire-protocol change - Desktop already sends `oauthToken`.

### Changed
- Updated reference materials (`bin/`, `vm-bundle/`) and docs (`COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md`) to Claude Desktop **v1.14271.0** (the `COWORK_RPC_PROTOCOL.md` validation was already recorded). No protocol or Go code changes: `cowork-svc.exe` is a byte-identical-size rebuild (new SHA `539b1618...`, VCS revision `c8f4d811b076` matching the `.latest` build hash, build `2026-06-18`; same Go 1.24.13, same handler set, same module deps, only build metadata and signing-cert dates rotated). The `app.asar` index.js diff is entirely minified-identifier churn (-777,201 bytes; no RPC method, spawn-param, event-type, or session/dispatch-keyword changes); the only new quoted constants are client-side experiment gates (`cowork_auto_permission_mode`, `cowork_bypass_permissions_mode`, `cowork_safety_banners`, `cowork_show_tool_permissioning_always_allow`, `cowork_argonaut_org_policies_main`) plus a Windows-only `CLAUDE_CODE_GIT_BASH_PATH` reference - none reach the spawn argv/env or the wire protocol, and the permission-mode flags pass through the native backend transparently. VM bundle unchanged (same SHA `6d1538ba`). Dependency bump only: Agent SDK 0.3.177 -> 0.3.181 (Electron 42.4.0, TypeScript ~6.0.3, MCP SDK 1.28.0 all unchanged). `chrome-native-host.exe` and `smol-bin.x64.vhdx` rebuilt (same sizes, new SHAs). Protocol stays at 23 active methods, 9 event types. `go vet` and `go test` all pass.
- Updated reference materials (`bin/`, `vm-bundle/`) and docs (`COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md`, `COWORK_RPC_PROTOCOL.md`) to Claude Desktop **v1.13576.1**. No protocol or Go code changes: `cowork-svc.exe` is a byte-identical-size rebuild (new SHA, VCS revision `772d01ffc175`, build `2026-06-17`; same handler set, same module deps, only build metadata and signing-cert dates rotated), the `app.asar` index.js diff is entirely minified-identifier churn (no RPC method, spawn-param, event-type, or session/dispatch-keyword changes), and the VM bundle is unchanged (same SHA `6d1538ba`). Dependency bumps only: Agent SDK 0.3.170 -> 0.3.177, TypeScript ~6.0.2 -> ~6.0.3 (Electron 42.4.0 and MCP SDK 1.28.0 unchanged). `chrome-native-host.exe` rebuilt (-8.7 KB). Protocol stays at 22 active methods, 9 event types. `go vet`, `go build`, and `go test` all pass.

## 1.0.60 — 2026-06-17

### Fixed
- **Live Artifacts render as garbled binary** (both backends): the `readFile` RPC returned the file content as raw text (`{"content": string(data)}`), but Desktop's Linux client always base64-decodes `response.result.content` (`Buffer.from(content,"base64")` at every call site, including the `create_artifact`/`update_artifact` `html_path` reader). With hostLoopMode forced off on Linux (so Cowork routes through this daemon), the artifact reader takes the VM branch and decoded valid HTML as base64, producing binary garbage that was written to the user's `Claude/Artifacts/<name>/index.html`; the `cowork-artifact://` protocol then served unparseable content and the renderer failed with `ERR_ABORTED (-3)`. The handler now base64-encodes the raw backend bytes (the upstream wire contract). `ReadFile` is unified to return raw bytes on both backends: the KVM guest-forward branch, which receives the guest's already-base64 content, now decodes it (defensively, tolerating a raw-returning guest) so the handler does not double-encode. ([#136](https://github.com/patrickjaja/claude-desktop-bin/issues/136), reported by [@draperbr](https://github.com/draperbr))
- **Live Artifacts fail to read the source HTML** (native backend): when `create_artifact`/`update_artifact` reads back an `html_path` that points inside the cowork scratch root, Claude Desktop prepends the cowork-home base (`<home>/.local/share/claude-cowork/`) to the already-absolute path one or more extra times before sending the `readFile` RPC (observed 2x and 3x), so the daemon opened a non-existent doubled path and the read failed - the artifact pane stayed blank / "File is outside allowed folders". `readFile` now collapses any run of that repeated cowork-home segment back to a single occurrence before opening the file. Scoped strictly to that segment, so arbitrary user paths are never rewritten. ([#136](https://github.com/patrickjaja/claude-desktop-bin/issues/136))

## 1.0.59 — 2026-06-17

## 1.0.58 — 2026-06-12

### Fixed
- **"Session not found" on the second message in Project sessions after idle** (native backend): the spawn cwd was picked from Go's randomized map iteration over `additionalMounts`, so sessions with multiple selected folders got a different cwd on each spawn. The CLI stores transcripts under `$CLAUDE_CONFIG_DIR/projects/<slug(cwd)>/` and resolves `--resume` only within the slug of its current cwd, so once the CLI process exited (idle), the next message's resume spawn looked under the wrong slug, failed with "No conversation found with session ID", and Desktop cleared `cliSessionId` and started a fresh session - orphaning the full transcript (still on disk, no longer referenced). The cwd choice is now deterministic (first `CLAUDE_CODE_WORKSPACE_HOST_PATHS` entry backed by an eligible mount, then mount-name order) and resume-aware: when `--resume <id>` is present, the candidate whose project slug already holds the transcript wins; if the transcript only exists under a slug no longer reachable (folder unmounted, or a KVM-era session resumed natively), it is copied - never moved or overwritten - into the chosen slug dir so the resume still works. Also fixes the sibling bug where a randomly-first mount failing its directory check left the cwd unoverridden entirely. Reported with detailed diagnostics by [@alex73630](https://github.com/alex73630) ([#66](https://github.com/patrickjaja/claude-cowork-service/issues/66))
- **KVM backend: native-era session state broke guest spawns, silently** (kvm backend): switching `COWORK_VM_BACKEND` from `native` to `kvm` with existing native sessions failed three ways ([#64](https://github.com/patrickjaja/claude-cowork-service/issues/64), reported by [@gianlucamazza](https://github.com/gianlucamazza)):
  - Native-era absolute symlinks (`sessions/<name>/mnt/*`, and nested-mount artifacts like `local_<id>/.claude/skills` created through the parent mount symlink) dangle inside the guest and made the guest sdk-daemon's mountpoint creation fail with `mkdir ... file exists`. The KVM spawn now sanitizes them host-side first (symlinks only - the native backend recreates them on demand; user files and directories are never touched)
  - Native-era transcripts live under host-path slugs while the guest CLI resolves `--resume` under `-sessions-<name>`, so resumes failed with "No conversation found". The transcript is now copied into the slug the guest will look under before the spawn is forwarded
  - A guest spawn failure (rejection, vsock down, 30s timeout) was converted into synthetic stderr/exit events plus a success response, so Desktop logged "Spawn succeeded" and dispatch/phone users got silence. It is now returned as a JSON-RPC error, which Desktop surfaces; a best-effort async kill reaps a guest process that may have launched late in the timeout case
- **present_files duplicate broken card** (native backend): the "NOTE: present_files cards may not be visible..." hint is now only appended for dispatch sessions (`CLAUDE_CODE_BRIEF=1`). Desktop's renderer treats every text content block in the present_files result as a file path (one card + `readLocalFile` per block), so in regular cowork sessions the hint rendered as a second, broken file card ("Unable to open file") and logged `INVALID_PATH` errors. Regular sessions now get pure path content blocks again, matching Desktop's built-in handler format; dispatch keeps the hint introduced in cec9ab6
- Removed the stdin readiness wait in the native backend (`writeStdin`): it waited for the CLI's first output before writing, but with `--input-format stream-json` (all Desktop-spawned sessions) the CLI emits nothing until it receives input, so the 5s timeout fired deterministically and added a fixed ~5s startup latency to every session (`Spawn succeeded` ~5030ms vs <1s). The write path already guards the real failure modes with its own 10s timeout and the `<-lp.done` process-exit cases; a broken pipe is only possible after process exit, which those guards cover. Especially visible on Dispatch, which spawns an orchestrator plus a child session per task. Contributed by [@gianlucamazza](https://github.com/gianlucamazza) ([#62](https://github.com/patrickjaja/claude-cowork-service/issues/62), [#63](https://github.com/patrickjaja/claude-cowork-service/pull/63))

### Added
- `pruneSessionCaches` RPC handler (new upstream method in v1.12603.0, called by Desktop's VM disk janitor every 300s, before spawns when disk is low, and from the manual disk cleanup menu) - routed through the `VMBackend` interface: the native backend prunes `.pre-stop-*` session backups (daemon-created by stopVM, up to five per session; session dirs and the CLI's caches in the user's real home are never touched), honoring `onlyIfFreeBytesBelow`, `includeSessionTmp`, and `sessionTmpOlderThanSeconds` (age from the timestamp in the backup name) and skipping running sessions; the KVM backend forwards it to the guest sdk-daemon which does the actual pruning inside the VM image
- Real `getSessionsDiskInfo` on native: statfs totals for the filesystem holding `~/.local/share/claude-cowork/sessions/` plus per-session `{name, sizeBytes}` entries (backups count toward their parent session). This activates Desktop's disk janitor and workspace-cleanup flow on native Linux, which previously short-circuited on the zero response
- Real `deleteSessionDirs` on native: deletes the named session dirs and their `.pre-stop-*` backups; rejects invalid names and sessions with a running process. Desktop's session cleanup dialog can now actually reclaim disk space
- API reachability probing (`probe` package): both backends probe `startVM.apiProbeURL` every 30s via HTTP HEAD and emit `apiReachability` events on status changes (reachable / probably_unreachable / unreachable); previously native emitted one static `reachable` and KVM emitted nothing. `startVM.cpuCount` is now applied to KVM sizing too (both params were parsed but dropped before)
- `oauthToken` field parsed from spawn params (new in v1.12603.0, mirrors `env.CLAUDE_CODE_OAUTH_TOKEN`; functionally ignored on native Linux where the token reaches the CLI via env)
- Spawn response now includes `failedMounts` - Desktop v1.12603.0 reads it to surface mount failures in the UI and retry them on resume. The native backend reports mount names whose symlink creation failed (intentional skips are not failures); the KVM backend passes through the guest sdk-daemon's list from its spawn ack (previously logged and dropped)
- `userDataRoot` parsed (and ignored) in `configure`/`subscribeEvents` params

### Changed
- Updated upstream reference materials from Claude Desktop v1.8555.2 to v1.12603.0
- Concurrent RPC dispatch per connection (`pipe/server.go`) - Desktop v1.12603.0 multiplexes all RPCs over one persistent connection, so slow handlers (e.g. kill's 1s delay, stopVM's session backup) no longer stall unrelated in-flight requests toward the client's 30s timeout; `subscribeEvents` still owns its connection synchronously
- VM bundle SHA changed for the first time since v1.1.9669: `5680b11b...` -> `6d1538ba6fecc4e5c5583993c4b30bb1875f0f5a` (all-new VM images, all file checksums changed)
- Electron 41.6.1 -> 42.4.0, Agent SDK 0.3.149 -> 0.3.170 (MCP SDK 1.28.0 unchanged); cowork-svc.exe itself is a metadata rebuild (same size, same go1.24.13, same handlers and dependencies, one new dispatch string `pruneSessionCaches`)
- Desktop no longer calls `isDebugLoggingEnabled`, `createDiskImage`, or `getDownloadStatus` over the pipe as of v1.12603.0 (our handlers remain for backward compatibility)
- New spawn env vars passed through transparently (v1.12603.0): `CLAUDE_CODE_DISABLE_REFUSAL_FALLBACK`, `CLAUDE_CODE_OAUTH_SCOPES`, populated `ANTHROPIC_CUSTOM_HEADERS`; new session type `radar`; literal `--brief` flag no longer used by Desktop (env `CLAUDE_CODE_BRIEF=1` remains)

- Updated upstream reference materials from Claude Desktop v1.7196.0 to v1.8555.2
- `installSdk` RPC params changed from `{name}` to `{sdkSubpath, version}` upstream (our handler is a no-op, no functional impact; struct already had the new fields)
- `handleCreateDiskImage` and `SetCondaDiskPath` removed from upstream binary strings (our no-op handlers remain for backward compatibility)
- Electron 41.6.1 (unchanged from v1.8089.0), Agent SDK 0.3.142 -> 0.3.149
- New spawn env vars passed through transparently (v1.8089.0): `CLAUDE_CODE_HOST_PLATFORM`, `TZ`, `ENABLE_PROMPT_CACHING_1H`, `CLAUDE_CODE_SUBAGENT_MODEL`
- New spawn env vars passed through transparently (v1.8555.2): `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `CLAUDE_CODE_DISABLE_TERMINAL_TITLE`, `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`, `CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL`, `CLAUDE_CODE_MAX_OUTPUT_TOKENS`, plus 9 `VERTEX_REGION_CLAUDE_*` region vars
- `--cowork` CLI flag added by plugin system for sub-commands only (not main session spawn)
- New client-side stdin backpressure handling: Desktop retries with error code `-32002` from `process.stdin` instead of failing (no Go changes needed)
- New artifact system (Electron-internal, zero wire impact on Go daemon)
- New Desktop-internal MCP tools: `archive_session`, `search_session_transcripts`, `list_connectors`, `list_plugins`, `suggest_skills`
- `BRIDGE_DISALLOWED_TOOLS` expanded: `mcp__cowork__create_artifact`, `mcp__cowork__update_artifact` added
- VM bundle unchanged - same SHA `5680b11bcdab215cccf07e0c0bd1bd9213b0c25d` since v1.1.9669

## 1.0.57 — 2026-05-14

## 1.0.56 - 2026-05-11

### Added
- Graceful shutdown with pre-kill backup - `kill()` now sends SIGINT first and waits 3s for the Claude CLI to flush pending writes before escalating to SIGTERM; `StopVM()` snapshots the session directory before killing processes (keeps 5 most recent backups)
- Startup integrity check for session JSONL files - on VM startup, scans the sessions directory for truncated/empty audit.jsonl files and logs warnings (does not auto-repair for safety)
- Session doctor tool (`scripts/cowork-session-doctor/`) - Python CLI for diagnosing and repairing session JSONL files (diagnose, repair, extract, backup, validate commands)

### Fixed
- Broken pipe race condition on spawn - adds a `ready` channel to `localProcess` that waits for first stdout/stderr output (up to 5s) before writing stdin, preventing `failed to flush buffered stdin` errors when Desktop sends data immediately after spawn confirmation

All changes in this section contributed by [@shmohammadi86](https://github.com/shmohammadi86) ([#41](https://github.com/patrickjaja/claude-cowork-service/pull/41)).

## 1.0.55 - 2026-05-11

### Fixed
- `isProcessRunning` response now includes `exitCode` field alongside `running` - Desktop expects both fields for process health monitoring
- Fix mount path canonicalization on systems where `/home` is a symlink to `/var/home` (Fedora Silverblue, Bazzite, CoreOS, Universal Blue) - `resolveSubpath()` now resolves symlinks when the fast string prefix check fails, preventing doubled paths like `/var/home/user/home/user/...` (#40)
- Fix `hostAbsFromShared()` in VM backend incorrectly rejecting valid paths as "outside home" on symlinked home systems
- Fix `ReadFile()` home containment check in VM backend to use canonicalized paths

### Changed
- Updated upstream reference materials to Claude Desktop v1.6608.1 (rebuild-only release - no protocol, handler, or VM bundle changes vs 1.6608.0)
- Updated upstream reference materials to Claude Desktop v1.6608.2 (rebuild with new tcpproxy dependency in cowork-svc.exe - no protocol, handler, or VM bundle changes vs 1.6608.1)

## 1.0.54 — 2026-05-07
- **Upstream update to Claude Desktop v1.6608.0** (from v1.6259.0)
- **Operon/Conda notebook engine completely removed from Desktop** - Massive internal refactoring that dropped the build size by approximately 3 MB. All conda-related code paths, the `createDiskImage` RPC method, and the `mountConda` spawn parameter are gone from the Desktop codebase.
- **`createDiskImage` RPC removed** - Desktop no longer sends this method. Our no-op handler remains for backward compatibility.
- **`spawn` no longer sends `mountConda` parameter** - The conda mount mode field was removed along with the Operon engine.
- **`addApprovedOauthToken` simplified** - Desktop now sends only `{token}` (the `name` field was removed).
- **`startVM` gains optional `cpuCount` and `apiProbeURL` fields** - New parameters for VM CPU allocation and API reachability probing. Ignored on native Linux.
- **`isDebugLoggingEnabled` now handled locally by Desktop** - No longer sent over the pipe. Our handler remains for backward compatibility.
- **New spawn env vars**: `CLAUDE_CODE_DISABLE_AGENTS_FLEET`, `CLAUDE_TMPDIR`
- **Removed spawn env var**: `CLAUDE_OAUTH_CLIENT_SECRET`
- **New locale**: id-ID.json (Indonesian) added to installer
- **New JS files**: `coworkArtifact.js` (new version), `buddy.js` added to app.asar build artifacts
- **Removed JS file**: `sqliteWorker.node.js` removed from app.asar
- **VM bundle**: Unchanged - same SHA (`5680b11b...`), same file checksums (stable since v1.1.9669)
- **Extract scripts migrated from Squirrel (nupkg) to MSIX** - The installer format changed from Squirrel (`lib/net45/resources/`) to MSIX (`app/resources/`). The extract scripts now use MSIX extraction, and `cowork-svc.exe` + `smol-bin.x64.vhdx` are properly extracted again (they were never removed - just moved to a different location in the installer package)
- **Updated reference docs** - COWORK_RPC_PROTOCOL.md, COWORK_SVC_BINARY.md, COWORK_VM_BUNDLE.md, CLAUDE.md, update-prompt.md updated to v1.6608.0

### Removed
- **`createDiskImage` RPC method** - No longer sent by Desktop (Operon/Conda removed). Handler retained as no-op for backward compatibility.
- **`mountConda` spawn parameter** - No longer sent by Desktop.
- **`name` field from `addApprovedOauthToken`** - Desktop now sends only `{token}`.

## 1.0.53 — 2026-05-05

### Added
- **`packaging/arch/build-pkg.sh`** — local pacman package builder. Mirrors the existing `build-deb.sh` / `build-rpm.sh` interface (`[--install] <binary> <version> [arch]`), generates a temporary PKGBUILD that wraps the prebuilt binary + systemd unit + install hook, runs `makepkg`, and drops a `claude-cowork-service-<ver>-1-<arch>.pkg.tar.zst` in the current directory. Pass `--install` to also `sudo pacman -U` the result.
- **`memoryGB` parameter on `StartVM`** — backend interface is now `StartVM(name, bundlePath, memoryGB)`. KVM honours the per-call override and falls back to the value from `Configure(memoryMB,...)` when the caller passes `0`; native ignores it.

### Changed
- **`StartVM` now receives the bundle path from the RPC layer** (`pipe/handlers.go`) — `pipe/handlers.go` forwards `params.BundlePath` to the backend so KVM can resolve the guest disk before launch instead of inferring it from the session name.
- **`Spawn` vfs bind failure now returns an RPC error** (`vm/backend.go`) — previously the bind error path synthesised a `stderr` + `exit code 1` pair on the process channel and returned `nil`, so Desktop saw a "process started, then died" sequence with no actionable error. We now return the bind error directly so `spawn` fails loud and Desktop's normal error handling kicks in.
- **Upstream update to Claude Desktop v1.6259.0** (from v1.5354.0)
- **Extract scripts migrated from Squirrel to MSIX** - Claude Desktop switched installer format from Squirrel (nupkg at `lib/net45/resources/`) to MSIX (at `app/resources/`). The extract scripts were updated to use MSIX extraction. `cowork-svc.exe` and `smol-bin.x64.vhdx` were never actually removed - they moved to the MSIX package structure. macOS now uses @ant/claude-swift native addon for VM management via Apple Virtualization.framework.
- **cowork-plugin-shim.sh removed from installer** - The plugin shim script is no longer bundled.
- **VM bundle**: Unchanged - same SHA (`5680b11b...`), same file checksums (stable since v1.1.9669)
- **app.asar**: SDK 0.2.121 -> 0.2.128. New build artifacts: buddy.js. New installer directories: fonts/, drizzle/sqlite/, ion-dist/, seed/.
- **New Desktop-side features** (no pipe protocol impact): Vertex auth renamed to interactive auth (triggerVertexAuth -> triggerInteractiveAuth), new IPC handlers (cancelQueuedMessage, resolveSSHSettings, submitFeedback), "cowork-desktop-dispatch" MCP server name removed, skills system expansion
- **IPC UUID changed**: `c0eed8c9-...` -> `04bc1015-...` (no protocol impact)
- **No new RPC methods** - all 22 methods, 8 event types, spawn parameters, and wire format are identical
- **No Go code changes needed**
- **Updated reference docs** - COWORK_RPC_PROTOCOL.md, COWORK_SVC_BINARY.md, COWORK_VM_BUNDLE.md updated to v1.6259.0
- **extract-cowork-svc.sh updated** - Script now extracts from MSIX package (`app/resources/`) instead of Squirrel nupkg (`lib/net45/resources/`), matching the current installer format

### Fixed
- **KVM exit event key mismatch** (`vm/bridge.go`) — the guest emits exit events as `{"type":"event","event":"exit","params":{"code":N,...}}`, but the native backend's `process.ExitEvent` uses `"exitCode"`. The bridge was forwarding the guest's `"code"` verbatim, so KVM-mode clients saw a different field name than native-mode clients. Both the nested-event form and the direct-event form now rename `code` → `exitCode` before emit.
- **`KvmBackend.allocateCID` race** (`vm/backend.go`) — the read-modify-write of `$baseDir/.next_cid` is now serialized with `syscall.Flock(LOCK_EX)` on the counter file. Without the lock, two concurrent daemons (daemon restart race, native↔kvm backend flip, user instance vs. systemd unit) — or two concurrent `StartVM` calls in the same process — could both read the same N and launch QEMU with duplicate `vhost-vsock-pci,guest-cid=N`. The kernel rejects the duplicate vsock binding, which surfaced as the generic `"QEMU exited immediately (check disk image or KVM access)"` error and pointed operators at disk/KVM instead of the real cause.
- **`KvmBackend.StartVM` TOCTOU on `b.started`** (`vm/backend.go`) — the initial `started` check now also sets a `b.starting` sentinel under `b.mu`, cleared via `defer`, so two concurrent `StartVM` calls can no longer both pass the gate and each run the full boot pipeline. Previously the second commit at the end of `StartVM` would overwrite `b.qemu` / `b.qmp` / `b.helper` / `b.bridge` / `b.watchdogStop`, orphaning a full QEMU + virtiofsd pair that `StopVM` could not reach.
- **`killStalePID` process identity check** (`vm/qemu.go`) — before SIGTERM/SIGKILL, confirm the PID's `/proc/<pid>/exe` points at a `qemu-system-*` binary. After a daemon crash (OOM, SIGKILL, power loss) that leaves `qemu.pid` on disk, PID reuse by any same-UID process (editor, compiler, browser tab) meant the next `StartVM` would silently signal-kill that unrelated workload. The `proc.Signal(0)` check only filtered by UID, not identity.
- **QMP connection leak on bridge-listen failure** (`vm/backend.go`) — the `bridge.Listen` error path now calls `qmp.Close()` (nil-guarded) in addition to `qemu.Shutdown(qmp)`. `Shutdown` only sends `system_powerdown` / `quit` over the QMP socket; it never closed the client FD. Because `StartVM` was returning an error, the caller never invoked `StopVM`, so the QMP Unix-socket FD was stranded for the life of the daemon until GC happened to finalize the unreachable `QmpClient`.
- **Subscriber slice unbounded growth** (`vm/backend.go`, `native/backend.go`) — `SubscribeEvents` now stores callbacks in a `map[uint64]func(event interface{})` keyed by a monotonic ID; cancel uses `delete`. The old `[]func` + `slot = nil` on cancel left a permanent tombstone per Desktop reconnect (suspend/wake, socket drop, Desktop restart), so `emit` paid an O(historical-reconnects) scan on every event — noticeable on busy builds emitting hundreds of stdout/stderr events per second.
- **Watchdog started before `StartVM` finished publishing state** (`vm/backend.go`) — the keepalive watchdog goroutine used to launch while `b.mu` was held, but the wait-for-guest path after the unlock could still fail and roll the backend back; the watchdog would then fire `StopVM` against half-initialised state. Watchdog launch now happens after the guest-connect wait, gated by a re-checked `b.started` under `RLock`, with `lastActivity` only seeded when we actually start it.
- **`runPendingSdkInstall` cleared the request before guest acked** (`vm/backend.go`) — the pending value was nilled-out under `Lock` before the `bridge.Forward` call, so a transient guest-side error dropped the install permanently. Now the pending struct is read under `RLock`, only cleared on success, and tracked by identity so a concurrent fresh `InstallSdk` doesn't get clobbered.
- **`GuestBridge.Forward` swallowed guest errors** (`vm/bridge.go`) — replies were unconditionally treated as success, so a guest-side `{"error": "..."}` payload was returned as raw bytes and parsed as a result. The pending channel now carries `(result, err)` and surfaces guest errors as Go errors with the guest's message string.
- **Helper accepted a stale virtiofsd socket as "ready"** (`vm/helper.go`) — `os.Stat` returned true the moment the socket inode existed on disk, which on a crashed-helper restart could be a leftover from the previous run before `virtiofsd` had bound to it. The helper now removes any pre-existing socket path before spawning virtiofsd and waits for `/proc/net/unix` to actually list the path before emitting `ready`, with the readiness loop also exiting on virtiofsd termination.
- **Process map leaked entries after exit** (`vm/backend.go`) — `emit` now deletes the matching entry from `b.processes` when forwarding an exit event (covering both `process.ExitEvent` and the wire-format `{type:"exit", id:...}` shape), so long-lived sessions no longer accumulate ghost process IDs.

All changes in this section contributed by [@mosi0815](https://github.com/mosi0815) ([#33](https://github.com/patrickjaja/claude-cowork-service/pull/33)).

## 1.0.52 — 2026-05-01

### Added
- **KVM backend** (`-backend=kvm`) — new QEMU/KVM-based guest runtime that replaces the old dormant VM implementation. Selectable via the `-backend` flag or the `COWORK_VM_BACKEND` environment variable. Listens on a dedicated socket (`cowork-kvm-service.sock`) so native and KVM daemons can coexist in the same `$XDG_RUNTIME_DIR`. Native remains the default. Contributed by [@mosi0815](https://github.com/mosi0815) ([#26](https://github.com/patrickjaja/claude-cowork-service/pull/26)).
  - `vm/backend.go` — session lifecycle, bundle preparation, memory/CPU configuration, process management
  - `vm/bridge.go` — vsock host↔guest JSON message bridge
  - `vm/qemu.go` — QEMU launch spec, root-disk boot (no more throwaway overlay), virtiofs `$HOME` share
  - `vm/qmp.go` — QMP control channel for live networking and shutdown
  - `vm/vfs.go` + `vm/helper.go` — VFS helper runs inside `unshare --user --map-root-user --mount` (invoked via `--vfs-helper` re-exec) to set up mounts without root on the host
  - `vm/preflight.go` — `CheckKvmPrerequisites()` gates startup on `/dev/kvm`, `qemu-system-x86_64`, and vhost-vsock
- **VHDX → qcow2 conversion caching** — root disk is converted once and reused across reboots. Base-image updates are detected via a trailer canary instead of a full SHA-256 scan, eliminating multi-second startup hashing. Contributed by [@mosi0815](https://github.com/mosi0815).
- **Shared session disk** — session state persists across all sessions of a given host instead of a per-host disk, matching upstream behavior. Contributed by [@mosi0815](https://github.com/mosi0815).
- **Log line truncation** — new `logx` package centralizes log output. Long JSON payloads (RPC params, `EVENT → client`, guest messages, `writeStdin` bodies, MCP-PROXY frames) are now truncated to 160 characters by default with a `…(+N more)` suffix showing how many characters were dropped. Previously these lines ran for thousands of characters or were truncated inconsistently at 200/300/500/2000/5000 characters by two near-duplicate helpers. Contributed by [@mosi0815](https://github.com/mosi0815).
- **`-log-full-lines` flag** — disables truncation globally for the session when you actually need the full payload. Also accepts `COWORK_LOG_FULL=1` environment variable as a fallback.
- **`-log-max-len` flag** — override the default 160-character budget.

### Changed
- **Upstream update to Claude Desktop v1.5354.0** (from v1.4758.0)
- **cowork-svc.exe**: Clean rebuild, same size (12,655,440 bytes), same Go version (go1.24.13). New SHA256 `026c6d2c163498e840b649049cbe3ce3fe451d9cac4dc1bf5077736b551f8cca`. Build date 2026-04-29, VCS revision `9a9e3d5a4a368f0f49a80dc303b0ed1a18bfedad`. No new RPC handler functions — identical handler set.
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums (stable since v1.1.9669)
- **app.asar**: SDK 0.2.119 → 0.2.121, TypeScript native-preview `7.0.0-dev.20260324.1` → `7.0.0-dev.20260414.1`. Electron 41.3.0 unchanged. New dependency `@ant/rfb-client` (Remote Framebuffer client). New `node-pty` module for PTY-based process spawning.
- **New Desktop-side features** (no pipe protocol impact): `FramebufferPreview` IPC (11 handlers — remote screen viewing via RFB), `Simulator` IPC (8 handlers — iOS simulator integration, macOS only), `CoworkArtifactBridge.runScheduledTask` (trigger scheduled tasks from artifacts), `CoworkMemory.resetMemories` (memory reset), `CoworkArtifacts.getArtifactThumbnail`, cloud-based memory sync system (23 new `cowork_memory_sync_*` telemetry events), `cowork_browser_cu_always_load` feature gate
- **Removed Desktop-side features** (no pipe protocol impact): `Custom3pSetup.copyManagedReport`, `Custom3pSetup.probeBootstrapUrl`, `bootstrapAuth` store (3 handlers), `triggerBootstrapAuth`
- **IPC UUID changed**: `305f54c0-...` → `c0eed8c9-...` (no protocol impact)
- **No new RPC methods** — all 22 methods, 8 event types, spawn parameters, and wire format are identical
- **No Go code changes needed**
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.5354.0

## 1.0.51 — 2026-04-25

### Changed
- **Upstream update to Claude Desktop v1.4758.0** (from v1.3883.0)
- **cowork-svc.exe**: Rebuild, same size (12,655,440 bytes), same Go version (go1.24.13). New SHA256 `4ccc771f26fd2db82b072f6cf4c61af2802a737940bf5d4436b9a7d28cd9cbc8`. New internal features: client binary signature verification (WinVerifyTrust), VHDX sparse disk creation, persistent bidirectional RPC, plugin permission gating, conda/session disk support, idle session cleanup, log file ACL hardening. New source files: `variant.go`, `signature.go`, `vhdx.go`, `logfile_security.go`.
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums (stable since v1.1.9669)
- **app.asar**: Updated. SDK 0.2.111 → 0.2.119, Electron 41.2.0 → 41.3.0, TypeScript ~5.8.3 → ~6.0.2. New workspace packages (`@ant/disclaimer`, `@ant/dxt-registry`, `@ant/utils`). New toolchain (oxlint/oxfmt). New `computerUseTeach.js` build artifact.
- **`configure` RPC now sends `userDataName` and `sessionOnly` fields** — Desktop sends a fire-and-forget `configure({userDataName: "Claude", sessionOnly: true})` on pipe connect since v1.4758.0. Go struct updated to accept these fields.
- **`subscribeEvents` RPC now sends `userDataName` field** — Go struct updated to accept this field.
- **New Desktop-side environment variables** (pass-through, no handler changes): `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `CLAUDE_CODE_CLASSIFIER_SUMMARY`, `CLAUDE_CODE_ENABLE_APPEND_SUBAGENT_PROMPT`, `CLAUDE_CODE_ENABLE_TASKS`, `CLAUDE_CODE_OTEL_HEADERS_HELPER_DEBOUNCE_MS`, `CLAUDE_CODE_RATE_LIMIT_TIER`, `CLAUDE_CODE_SUBSCRIPTION_TYPE`, `CLAUDE_COWORK_MEMORY_GUIDELINES`, `CLAUDE_FORCE_HOST_LOOP`
- **Removed env vars**: `CLAUDE_CODE_PROXY_RESOLVES_HOSTS`, `CLAUDE_INTERNAL_FC_OVERRIDES`, `CLAUDE_RPC_TOKEN`
- **New Desktop-side features** (no pipe protocol impact): `askClaude` CoworkArtifact bridge (replaces `sample`), `openExternalUrl` artifact method, SSH transport backend, config management API (`createConfig`/`readConfig`/`writeConfig`/`deleteConfig`), git worktree operations, `setFastMode`, `setDeploymentMode`, session adoption/classification, space summarization, `parkAndCapture` artifacts, `probeEgressHosts`/`probeBootstrapUrl` connectivity probing
- **IPC UUID changed** (no protocol impact)
- **Feature gate count**: 43 → 58 (15 new gates)
- **No new RPC methods** — all 22 methods, 8 event types, spawn parameters, and wire format are identical
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.4758.0

### Added
- **`userDataName` field in `configureParams` struct** (`pipe/handlers.go`) — accepts the new parameter from Desktop v1.4758.0
- **`sessionOnly` field in `configureParams` struct** (`pipe/handlers.go`) — accepts fire-and-forget on-connect configure calls
- **`userDataName` field in `vmNameParams` struct** (`pipe/handlers.go`) — accepts the new parameter in subscribeEvents

## 1.0.50 — 2026-04-22

### Changed
- **Upstream update to Claude Desktop v1.3883.0** (from v1.3561.0)
- **cowork-svc.exe**: Minor rebuild (+512 bytes, 12,654,928 → 12,655,440 bytes), same Go version (go1.24.13). Build date 2026-04-21, VCS revision `93ff6cb984386882b4bd9b6bca80d4cf5af8e13b`. New `configure: %w` error wrapping (replaces `Config %`). No new RPC handler functions.
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **app.asar**: Grew significantly (~23 MB → ~28 MB, +19.5%). All changes client-side; pipe protocol unchanged.
- **claude-agent-sdk**: Unchanged at 0.2.111. MCP protocol version 2.1.111. Electron unchanged.
- **`default.clod` removed** from installer — no longer shipped
- **New Desktop-side features** (no pipe protocol impact): `coworkArtifacts` feature flag, `coworkSpaceContextEnabled` setting, `DebugHandoff` URL handler, `list_connectors` internal MCP tool, multi-plugin `suggest_plugin_install` schema, `present_files` atomic file writes, OAuth localhost HTTP support, title-gen `--model` flag, `dispatch_child` gains space/directory/active_children fields, `RemoteProcess.rebind()` gains `canReattach`, `PluginOAuthStorage.clientConfig` key
- **IPC UUID change**: `df0aa1df...` → `4ab9ae55-583a-4867-90be-23b2daff8899` (no protocol impact)
- **No Go code changes needed for protocol** — all 22 RPC methods, 8 event types, spawn parameters, and wire format are identical
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.3883.0

### Fixed
- **Reverse mount path remapping applied unconditionally** — `streamOutput()` applied `reverseMountRemap` (real host paths → VM `/sessions/` paths) even when `reverseMap=false` (native Linux without root). This caused bash command output (e.g., `wc -l` filenames) to contain `/sessions/<name>/mnt/...` paths that don't exist on disk, breaking subsequent model tool calls. Now both mount-level and session-level reverse mapping are gated behind the `reverseMap` flag.
- **`readFile` RPC parameter mismatch** — Desktop sends `{processName, filePath}` and expects `{content}` in response. Our handler was parsing `{name, path}` and returning `{data}`. Fixed JSON tags and response field name. (Pre-existing bug, not introduced by v1.3883.0)
- **`mountPath` RPC parameter mismatch** — Desktop sends `{processId, subpath, mountName, mode}`. Our handler was parsing `{name, hostPath, guestPath}`. Fixed JSON tags and updated VMBackend interface. (Pre-existing bug, not introduced by v1.3883.0; mountPath is a no-op in native mode so this had no functional impact)

### Changed (prior releases in this cycle)
- **Prior upstream update to Claude Desktop v1.3561.0** (from v1.3109.0)
- **cowork-svc.exe**: Minor rebuild (+6,656 bytes, 12,648,272 → 12,654,928 bytes), same Go version (go1.24.13). Build date 2026-04-20, VCS revision `fbc74be3fdc714a2c46ef1fb84f71d4e4c062930`. No new RPC handler functions; certificate date rotation in embedded TLS certs.
- **Default socket path depends on backend** — native keeps the historical `cowork-vm-service.sock` for Desktop compatibility; KVM uses `cowork-kvm-service.sock` so Desktop can tell the two modes apart by which socket exists.
- **Logging call sites consolidated** — `pipe/handlers.go` (RPC dispatch, `handleWriteStdin`, `handleSpawn`, `handleSubscribeEvents`), `vm/bridge.go`, `vm/backend.go`, and `native/process.go` now route through `logx.Debug` / `logx.Info`, removing scattered `if h.debug { log.Printf(...) }` wrappers. The `setDebugLogging` RPC still toggles debug output at runtime.
- **Retired duplicate truncation helpers** — `vm/bridge.go#truncate` and `native/process.go#truncateLine` are gone; all call sites use `logx.Trunc`.
- **MCP-PROXY detection logs are now gated** — `[native] >>>MCP-PROXY>>>`, `<<<MCP-PROXY<<<`, and `<<<MCP-INIT<<<` lines emit only under `-debug`, matching the documented "quiet by default" behavior.
- **VM bundle**: Unchanged — same SHA (`5680b11b...`), same file checksums
- **app.asar**: Updated, all changes are minifier symbol renames. All 22 RPC methods still referenced; session dispatch logic unchanged.
- **claude-agent-sdk**: 0.2.92 → 0.2.111; MCP protocol version 2.1.111. Electron 41.2.0 unchanged.
- **New Desktop-side features** (no pipe protocol impact): `EnabledCliOpsStore` (persistent CLI ops tracking), `coworkTrustedDeviceToken` (encrypted device tokens, gate `2023768496`), `is_child` session listing field (`dispatchParentOrigin`), SSH remote spawn feature flag `1496676413` for plugin/MCP passthrough, `lam_tool_permission_responded` telemetry, standalone title-gen spawn path
- **IPC UUID change**: `8e6f15c2...` → `df0aa1df-1260-46ce-9bc9-e094b676df19` (no protocol impact)
- **No Go code changes needed** — all 22 RPC methods, 8 event types, spawn parameters, and wire format are identical
- **Updated reference docs** — `COWORK_RPC_PROTOCOL.md`, `COWORK_SVC_BINARY.md`, `COWORK_VM_BUNDLE.md` updated to v1.3561.0
- **Prior upstream update to Claude Desktop v1.3109.0** (from v1.3036.0, commit `cfc2153`): clean rebuild, byte-identical binary size, no protocol changes
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
