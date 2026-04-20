# Cowork Service Binary Analysis — v1.3561.0

## Binary Overview

- **Windows**: cowork-svc.exe — Go binary (~11 MB), implements Hyper-V VM management
- **macOS**: cowork-svc — Go binary (~4.5 MB), implements Apple Virtualization framework
- Both bundled inside Claude Desktop installer at `lib/net45/` level

## Extracted Files (bin/ directory)

The extract script pulls all files from the same directory level as cowork-svc.exe:

| File | Size | Purpose |
|------|------|---------|
| cowork-svc.exe | 12 MB | Windows Hyper-V backend (Go binary) |
| app.asar | 23 MB | Claude Desktop Electron app (same as main app) |
| chrome-native-host.exe | 1 MB | Chrome native messaging host for browser tools |
| cowork-plugin-shim.sh | 7.5 KB | Plugin permission gating library (new in v1.1.9669, updated in v1.2581.0) |
| smol-bin.x64.vhdx | 36 MB | Empty ext4 filesystem for sdk-daemon updater |
| default.clod | 97 KB | Default configuration/data |
| *.json (locale files) | ~15-75 KB each | UI translations (de-DE, en-US, es-419, etc.) |
| *.png / *.ico | ~2-4 KB each | Tray icons (light/dark, various DPI) |
| .version | 8 bytes | Version string ("1.3561.0") |

## Windows Architecture

```
Claude Desktop (Electron)
  -> Named Pipe (\\.\pipe\cowork-vm-service)
    -> cowork-svc.exe (Go)
      -> Hyper-V API
        -> Linux VM (rootfs.vhdx + vmlinuz + initrd)
          -> sdk-daemon (vsock, port 51234/0xC822)
            -> Claude Code CLI
```

## macOS Architecture

```
Claude Desktop (Electron)
  -> Unix Socket
    -> cowork-svc (Go, Swift bindings)
      -> Apple Virtualization.framework
        -> Linux VM (rootfs.img)
          -> sdk-daemon (vsock)
            -> Claude Code CLI
```

## Linux Native Architecture (Our Implementation)

```
Claude Desktop (Electron, patched)
  -> Unix Socket ($XDG_RUNTIME_DIR/cowork-vm-service.sock)
    -> cowork-svc-linux (Go, this project)
      -> Direct host execution (os/exec)
        -> Claude Code CLI
```

## Protocol Differences Between Platforms

| Aspect | Windows | macOS | Linux (ours) |
|--------|---------|-------|-------------|
| Transport | Named Pipe | Unix Socket | Unix Socket |
| VM | Hyper-V | Apple Virtualization | None (native) |
| Guest comms | HVSocket (AF_HYPERV) | vsock (AF_VSOCK) | N/A (direct exec) |
| vsock port | 0xC822 (51234) | 0xC822 (51234) | N/A |
| Binary | cowork-svc.exe (Go) | cowork-svc (Go+Swift) | cowork-svc-linux (Go) |
| Bundle | rootfs.vhdx + vmlinuz + initrd | rootfs.img | None needed |

---

## cowork-svc.exe Deep Analysis (v1.3561.0)

| Property | Value |
|----------|-------|
| **File type** | PE32+ executable for MS Windows 6.01 (console), x86-64, 8 sections |
| **Go version** | go1.24.13 |
| **Module** | github.com/anthropics/cowork-win32-service |
| **Build date** | 2026-04-20 |
| **Size** | 12,654,928 bytes |
| **SHA256** | 84758c5a16891aeee1b59800608b260948f0f5c5efd8c8994fba407edc5684d8 |

### Go Module Structure (from binary strings)

Three packages: `main`, `pipe`, `vm`

#### pipe package (RPC protocol handling)

**Server lifecycle:**
- `pipe.NewServer`, `pipe.(*Server).Start`, `pipe.(*Server).Stop`
- `pipe.(*Server).acceptLoop`, `pipe.(*Server).handleConnection`

**Request dispatch:**
- `pipe.(*Server).dispatch`, `pipe.(*Server).dispatchVerified`, `pipe.(*Server).dispatchWithSession`

**Session management:**
- `pipe.(*Server).getOrCreateSession`, `pipe.(*Server).getSessionForConn`
- `pipe.(*Server).checkIdleSessions`, `pipe.(*Server).idleSessionChecker`
- `pipe.(*vmSession).broadcast`, `pipe.(*vmSession).isConfigured`, `pipe.(*vmSession).subscriberCount`

**RPC handlers:**
- handleConfigure
- handleCreateVM
- handleStartVM
- handleStopVM
- handleSubscription
- handleWriteStdin
- handleIsRunning
- handleIsGuestConnected
- handleIsProcessRunning
- handleIsDebugLoggingEnabled
- handleSetDebugLogging
- handleCreateDiskImage
- handleSendGuestResponse *(new in v1.569.0)*
- handlePassthrough
- handlePersistentRPC

**Wire protocol:**
- `pipe.ReadMessage`, `pipe.WriteMessage`

**Windows security:**
- `pipe.(*Server).InitSignatureVerification`, `pipe.(*Server).verifyClientSignature` — code signing verification
- `pipe.calculateCertThumbprint`, `pipe.getSigningCertificateInfo` — Windows code signing
- `pipe.GetClientInfo`, `pipe.GetClientInfoFromConn` — caller authentication
- `pipe.getPackageFamilyName` — UWP/MSIX package identity
- `pipe.getUserProfileDirectory`, `pipe.lookupSID` — Windows user identity

#### vm package (Hyper-V management)

**VM lifecycle (`vm.(*WindowsVMManager)`):**
- CreateVM, StartVM, StartVMWithBundle, StopVM
- IsRunning, IsGuestConnected, IsProcessRunning

**Filesystem sharing:**
- AddPlan9Share — 9P filesystem sharing (host -> VM)

**Process management:**
- ForwardToVM, WriteStdin

**VM configuration:**
- SetMemoryMB, SetCPUCount, SetKernelPath, SetInitrdPath, SetVHDXPath
- SetSmolBinPath, SetSessionDiskPath, SetCondaDiskPath — disk management
- SetUserToken, SetOwner — Windows user context
- SetEventCallbacks, emitStartupStep

**TLS/CA:**
- installHostCACertificates — TLS CA injection
- `vm.LoadTrustedCACertificates` — host CA cert loading (refactored in v1.1062.0 to use `enumerateRootStore` helper; gained an extra closure `Printf.func4` in v1.3036.0)
- `vm.enumerateRootStore` — *(new in v1.1062.0)* Windows certificate root store enumeration
- `vm.enumerateCertStore` — *(new in v1.3036.0)* generalized Windows certificate store enumeration (beyond just root store)
- `vm.certChainsToTrustedRoot` — *(new in v1.3036.0)* builds certificate chains to trusted root via `windows.CertGetCertificateChain` / `CertFreeCertificateChain`

**HCS (Host Compute Service) API:**
- `vm.CreateComputeSystem`, `vm.OpenComputeSystem`, `vm.EnumerateComputeSystems`
- `vm.(*HCSSystem)` — Start, Shutdown, Terminate, Close, GetProperties, ModifyComputeSystem, AddPlan9Share
- `vm.(*VMConfig).BuildHCSDocument` — HCS configuration generation

**vsock RPC to sdk-daemon (`vm.(*RPCServer)`):**
- acceptLoop, handleConnection, handleMessage, handleEvent, handleResponse
- SendRequestAndWait, SendNotification, SendInstallCACertificates, writeFrame
- IsConnected, SetCallbacks, Start, Stop

**Hyper-V sockets:**
- `vm.(*HVSocketListener)`, `vm.(*HVSocketConn)` — AF_HYPERV socket types

**Console/networking:**
- `vm.(*ConsoleReader)` — VM console output capture
- `vm.(*VirtualNetworkProvider)` — HCN networking

**VM lifecycle utilities:**
- `vm.CleanupStaleVMs`, `vm.VMIDForSID`, `vm.isOurVM`
- `vm.CreateSparseVHDX` — dynamic disk creation
- `vm.VsockPortToServiceGUID`, `vm.NetworkVsockServiceGUID` — GUID mapping

**Path security:**
- `vm.ValidateWritePath`, `vm.validateLogPath`

### External Dependencies

- `github.com/apparentlymart/go-cidr/cidr` — CIDR arithmetic for networking
- `github.com/containers/gvisor-tap-vsock` — gVisor networking (DHCP, DNS, forwarder)
- `golang.org/x/net/http2` — HTTP/2 support

### Notable Methods Not in Our Handler

| Method | Purpose | Notes |
|--------|---------|-------|
| `handlePassthrough` | Forwards arbitrary requests to VM | We handle all methods directly |
| `handlePersistentRPC` | Long-lived bidirectional RPC | May be used for future streaming features |
| `SetCondaDiskPath` | Conda environment management | Native Linux uses host conda directly |

**Newly handled in v1.1.9669:** `handleCreateDiskImage`, `getSessionsDiskInfo`, `deleteSessionDirs` (all no-ops on native Linux).

**v1.2.234:** No new handler functions. Binary is a rebuild with updated timestamps only (identical size).

**v1.3561.0:** Minor rebuild (+6,656 bytes, 12,648,272 → 12,654,928 bytes). Same Go version (go1.24.13). No new handler functions. Build date 2026-04-20, VCS revision `fbc74be3fdc714a2c46ef1fb84f71d4e4c062930`. Certificate date rotation visible in string diff. No new RPC methods.

**v1.569.0:** New handler `handleSendGuestResponse` for plugin permission bridge guest responses. Binary grew ~11KB.

**v1.1062.0:** No new handlers. Internal cert handling refactored (`LoadTrustedCACertificates` → `enumerateRootStore`). New `vm/rpc_types.go` source file (type refactor, not new types). Binary shrank 8KB.

**v1.1348.0:** No new handler functions. Rebuild only — same size (11,177,808 bytes), same Go version (go1.24.13). Updated build timestamps and VCS revision.

**v1.1617.0:** No new handler functions. Rebuild with minor size increase (11,177,808 → 11,179,344 bytes, +1.5 KB). Same Go version (go1.24.13). Updated build timestamps, VCS revision, and embedded TLS certificates (date rotation).

**v1.2278.0:** No new RPC handler functions. Binary grew significantly (11,179,344 → 12,643,664 bytes, +13.1%) due to linking full `net/http`, `crypto/tls`, and `compress/flate` packages for new WPAD/PAC proxy auto-discovery feature. Same Go version (go1.24.13). New VM-internal features: `detectAutoProxyConfigURL`, `fetchPACScript`, `resolveWPADAndResend` (proxy auto-config for VM guest). New JSON fields: `pacScript` and `hostLoopbackIP` (both `omitempty`, VM-internal proxy config). New string `enableSessionEvents` found but not yet called by Desktop. `HVSocketConn.SetWriteDeadline` added.

---

## bin/ Directory Checksums (v1.3561.0)

| File | SHA256 |
|------|--------|
| cowork-svc.exe | 84758c5a16891aeee1b59800608b260948f0f5c5efd8c8994fba407edc5684d8 |
| cowork-plugin-shim.sh | 2fbef5ee6c07c26a1f7cd9204e1b6d37537edd2b96c0ce025010b890cb5935e7 *(unchanged from v1.2773.0)* |
| chrome-native-host.exe | d7852a8d49252f94c5e95e853ed0754033f4b5bc64f030593c1abaaa19644b97 |
| smol-bin.x64.vhdx | 7ca9598f6daa1d8a53095b95d3c57e1f3db3df47dbd755df4e8efb786deac6cf |
| default.clod | d601ae9bf53de2d6d4a202c3fef1bd9ef2898932483e9df6a6a3dd99eb240796 |
| app.asar | 7f5eb546e0275eeb92ec34cbb27b57146072976b4873a645342157d8946195de |

---

## app.asar Analysis (from bin/)

| Property | Value |
|----------|-------|
| **Package** | @ant/desktop v1.3561.0 |
| **Electron** | 41.2.0 |
| **Node requirement** | >=22.0.0 |

### New in v1.1.9669

- **coworkArtifact.js** — Electron preload script exposing `window.cowork.callMcpTool(toolName, params)` bridge for web artifacts to invoke MCP tools
- **Plugin/marketplace system** — Full plugin install/uninstall/sync via Electron IPC (`CustomPlugins` interface), not cowork-svc RPC
- **Conda integration** — `createDiskImage` RPC, `mountConda` spawn param, `manage_environments`/`manage_packages` tools
- **Scheduled tasks** — `coworkScheduledTasksEnabled` / `ccdScheduledTasksEnabled` settings (both default `false`)
- **New cowork tools**: `request_network_access`, `request_host_access`, `render_dashboard`/`patch_dashboard`/`read_dashboard`, `display_artifacts`
- **`--cowork` flag** — appended to CLI commands when `useCoworkFlag` is true

### New in v1.2.234

- **`dispatchCodeTasksPermissionMode`** — New preference controlling permission mode for dispatch code tasks: `"default"`, `"acceptEdits"`, `"plan"`, `"auto"`, `"bypassPermissions"` (default: `"acceptEdits"`)
- **`start_code_task` MCP tool** — New dispatch tool for code-specific tasks (in addition to `start_task`). Desktop prefers this for code work (editing repos, running tests)
- **Plugin permission bridge mounts** — Desktop now passes `.cowork-perm-req` (rw) and `.cowork-perm-resp` (ro) in `additionalMounts` for plugin confirmation protocol
- **`.cowork-lib` shim mount** — Plugin shim library mounted read-only at `mnt/.cowork-lib/shim.sh` for plugins to source
- **`remotePluginsPath`** — New internal Desktop parameter used to construct `additionalMounts` (not passed directly via RPC)
- **Electron 40.8.5** — Upgraded from 40.4.1
- **claude-agent-sdk-future 0.2.90-dev** — Updated from 0.2.86-dev

### New in v1.569.0

- **`sendGuestResponse` RPC method** — New handler in cowork-svc.exe for delivering host responses to VM guest processes (plugin permission bridge)
- **`navigateHost` IPC** — New CoworkArtifactBridge method for host navigation from artifacts
- **OperonSkills IPC** — Full CRUD for skills management (create, createFromFile, delete, get, list, listForAgent, update, attachAgents, detachAgent)
- **Local skills management** — New `saveLocalSkill`, `deleteLocalSkill`, `revealLocalSkill` handlers
- **SideChat** — New side-chat spawning functionality
- **Dispatch improvements** — `translateDispatchAttachments`, `startDispatchChildSession`, `detachDispatchChildren`
- **Teaching mode** — `cu_teach_session` telemetry events
- **Artifact lifecycle** — New telemetry events: `cowork_artifacts_created`, `cowork_artifacts_updated`, `cowork_artifacts_imported`, `cowork_artifacts_exported`
- **IPC UUID change** — Internal Electron IPC bridge UUID changed (no protocol impact)
- **SDK versions unchanged** — Same Electron 40.8.5, same claude-agent-sdk versions

### New in v1.3561.0

- **cowork-svc.exe**: Minor rebuild (+6,656 bytes, 12,648,272 → 12,654,928 bytes). Same Go version (go1.24.13). VCS revision `fbc74be3fdc714a2c46ef1fb84f71d4e4c062930`, build timestamp `2026-04-20T14:59:51Z`. No new RPC handler functions. Certificate date rotation in embedded TLS certs.
- **app.asar**: Updated. Diff is overwhelmingly minifier symbol renames — all 22 RPC methods still referenced; session-type dispatch unchanged.
- **VM bundle unchanged** — same SHA `5680b11bcdab215cccf07e0c0bd1bd9213b0c25d`, all file checksums identical.
- **claude-agent-sdk** updated: 0.2.92 → 0.2.111. MCP protocol version 2.1.111.
- **Electron 41.2.0** — unchanged.
- **New Desktop-side features** (no pipe protocol impact):
  - **`EnabledCliOpsStore`** — new persistent store (`cowork-enabled-cli-ops`, `configFileMode:384`) for tracking enabled CLI operations
  - **`coworkTrustedDeviceToken`** — trusted device token storage with encryption (gate `2023768496`), 10-second timeout
  - **`is_child` session listing field** — `listAllSessions()` now returns `isChild` and `dispatchParentOrigin` fields for remote dispatch session tracking
  - **SSH remote spawn** — feature flag `1496676413` now gates plugin/MCP passthrough for SSH-spawned sessions (`createSpawnFunction` gains second parameter)
  - **`coworkWebFetchViaApi`** toggle — web fetch routing now dynamically switchable via feature flag
  - **`cu_lock_released` / `cu_teach_session`** telemetry — computer-use lock and teach-mode tracking (continued from v1.3109.0)
  - **`lam_tool_permission_responded`** — new telemetry event for permission responses in cowork sessions
  - **Title generation** — new standalone title-gen spawn path for session titles
- **IPC UUID change** — `8e6f15c2-1794-4f6a-a9e4-7586203a8d91` → `df0aa1df-1260-46ce-9bc9-e094b676df19` (no protocol impact)
- **No new RPC methods** — Protocol remains at 22 methods and 8 event types. Wire format, spawn parameters, and event structures are identical to v1.3109.0. No Go code changes required.

### New in v1.3109.0

- **cowork-svc.exe**: Clean rebuild, **byte-identical size** (12,648,272 bytes), same Go version (go1.24.13). Only build metadata changed (VCS revision `35cbf6530e05912137624cde0f075dc7f121fa60`, build timestamp `2026-04-16T20:32:01Z`). No new handler functions, no new error strings, no structural diff in the binary beyond linker-placement noise.
- **app.asar**: Significantly grew (10.1 MB → 14.6 MB extracted `index.js`). Contents are overwhelmingly minifier symbol renames — all 22 of our RPC methods still referenced; session-type dispatch unchanged.
- **VM bundle unchanged** — same SHA `5680b11bcdab215cccf07e0c0bd1bd9213b0c25d`, all file checksums identical.
- **SDK versions unchanged** — Electron 41.2.0, claude-agent-sdk 0.2.92, claude-agent-sdk-future 0.2.93-dev, @modelcontextprotocol/sdk 1.28.0.
- **No new RPC methods** — Protocol remains at 22 methods and 8 event types. Wire format, spawn parameters, and event structures are identical to v1.3036.0. No Go code changes required.

### New in v1.3036.0

- **cowork-svc.exe**: Minor rebuild (+4,096 bytes, 12,644,176 → 12,648,272 bytes). Same Go version (go1.24.13). New Windows-only certificate store helpers: `vm.enumerateCertStore`, `vm.certChainsToTrustedRoot` (use `windows.CertGetCertificateChain` / `CertFreeCertificateChain`). `vm.LoadTrustedCACertificates` gained one additional closure (`Printf.func4`). New error string `"[VM] Failed to load host CA certificates: %v"`. No new RPC handler functions.
- **`ENABLE_PROMPT_CACHING_1H=1`** — New environment variable injected by Desktop into every spawned Claude Code process (alongside `CLAUDE_CODE_IS_COWORK=1`, `DISABLE_MICROCOMPACT=1`, etc.). Passed through transparently by our backend — no code change required.
- **`cowork-plugin-oauth` storage** — New local `conf` store (`new CD({name:"cowork-plugin-oauth",configFileMode:384})`) under `[PluginOAuthStorage]` for per-plugin OAuth credentials. Desktop-side local file, not a pipe RPC.
- **Cowork artifact lifecycle events** — New `[CoworkArtifacts] Created` and `[CoworkArtifacts] Imported` log lines alongside existing `Updated`/`Exported`. New telemetry: `cowork_artifacts_created`, `cowork_artifacts_imported`.
- **`cu_lock_released`, `cu_teach_session`, `lam_mcp_servers_setup_summary`** — New telemetry events for computer-use lock tracking, teach-mode sessions, and MCP server setup summaries.
- **`cowork_lock_midsession_model`** — New feature gate — when enabled, prevents mid-session model changes in cowork sessions (Desktop-side enforcement only).
- **Feature gate `3444158716`** — New gate keyed on `sessionType==="cowork"` (purpose not fully identifiable from minified code).
- **GrowthBook gates** — New top-level gates `louderPenguin` and `operon` added alongside existing `coworkKappa` (non-cowork features).
- **`setup-cowork` skill** — New built-in skill command (`{name:"setup-cowork",description:...,prompt:...}`) driven by feature flag `4066504968`/`skillPrompt`.
- **IPC UUID change** — Internal Electron IPC bridge UUID changed (`f189fbc9...` → `08aa66e6-e7d3-4eb8-95ac-7e3f613ce196`), no protocol impact.
- **SDK versions unchanged** — Same Electron 41.2.0, same claude-agent-sdk 0.2.92, claude-agent-sdk-future 0.2.93-dev.
- **No new RPC methods** — Protocol remains at 22 methods and 8 event types. Wire format, spawn parameters, and event structures are identical to v1.2773.0.

### New in v1.2773.0

- **SDK versions rolled back** — claude-agent-sdk 0.2.92 (was 0.2.101), claude-agent-sdk-future 0.2.93-dev (was 0.2.102-dev). All other dependency versions unchanged (Electron 41.2.0, conway-client, @modelcontextprotocol/sdk 1.28.0)
- **cowork-svc.exe**: Minor rebuild (+512 bytes), same Go version (go1.24.13), no new RPC handler functions. Installer directory path moved from `lib/net45/` to `lib/net45/resources/`
- **`[cowork-deletion]` event logging** — Desktop now POSTs session deletion events to API with retry logic (up to 5 attempts with backoff). Desktop-internal telemetry, not a pipe RPC
- **`dispatchOnCliOpAlwaysAllowed`** — New renderer dispatch event for CLI operations that skip permission checks (Desktop-side)
- **`coworkWebSearchEnabled` gate removed** — Web search gate check removed from session startup path; web search now always enabled for cowork sessions
- **IPC UUID change** — Internal Electron IPC bridge UUID changed (`f189fbc9...`), no protocol impact
- **Plugin shim unchanged** — Same SHA (`2fbef5ee...`), same behavior
- **No new RPC methods** — Protocol remains at 22 methods and 9 event types

### New in v1.2278.0

- **Electron 41.2.0** (was 40.8.5) — major Electron upgrade
- **SDK versions**: claude-agent-sdk 0.2.101 (was 0.2.92), claude-agent-sdk-future 0.2.102-dev (was 0.2.93-dev), conway-client unchanged
- **cowork-svc.exe**: +13.1% binary size (11.2→12.6 MB) due to new WPAD/PAC proxy auto-discovery; same Go version (go1.24.13); no new RPC handler functions
- **`window.cowork.sample()`** — New artifact API allowing dashboard artifacts to call Claude for lightweight synthesis (Desktop-side IPC, not pipe protocol)
- **`coworkWebFetchViaApi` feature flag** — Routes `web_fetch` tool calls through Anthropic API instead of VM-direct fetch; adds `mcp__workspace__web_fetch` as workspace tool (Desktop-side)
- **`vmEgressPolicy()`** — New Desktop method returning `{kind:"unrestricted"}`, `{kind:"allowlist",domains:[...]}`, or `null` for DNS/egress filtering (Desktop-side)
- **`forkSession` / `forkAtMessageUuid`** — New session forking IPC (Desktop-side)
- **`rewind`** — New session rewind IPC (Desktop-side)
- **`summarizeTranscript`** — New transcript summarization IPC (Desktop-side)
- **`readSessionImageAsDataUrl`** — New session image reading IPC (Desktop-side)
- **Vertex Auth** — New `triggerVertexAuth`, `revokeVertexAuth` IPC handlers for Google Vertex AI enterprise auth (Desktop-side)
- **`setBundledSkills`** — New IPC for skill management (Desktop-side)
- **`CoworkRadar setCardStatus`** — Replaces `markCompleted` (Desktop-side rename)
- **Skills staging in plugin shim** — Plugin shim now has access to bundled skills via `V9n()` staging
- **Memory guidelines update** — Extra privacy guidelines appended for bridge sessions via `CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES`
- **New session files** — `cowork-gb-cache.json` (GrowthBook feature flag cache), `cowork_account_settings.json` (account-level settings)
- **No new RPC methods** — Protocol remains at 22 methods and 8 event types

### New in v1.1617.0

- **`coworkEgressAllowedHosts` admin setting** — New enterprise/MDM-configurable egress allowlist that merges into the existing `allowedDomains` spawn parameter. Desktop resolves the merge before sending via RPC, so no cowork-svc protocol change needed
- **`canUseTool` VM path guard** — Desktop now blocks host-loop tools from operating on `/sessions/` paths (Desktop-side enforcement, no protocol impact)
- **`cowork-plugin-shim.sh` integration** — Desktop now copies the shim script into the `.cowork-lib` mount during session setup (was previously present as a file but not actively copied)
- **`_syncPlugins` timeout** — Plugin sync now uses a 5-second timeout for account resolution
- **`getSessionStorageDir` replaces `mountFolder`** — Internal Desktop refactor of how the cowork MCP tools factory gets session context (no RPC protocol change)
- **`request_cowork_directory` storage guard** — Desktop prevents mounting the session's own internal storage directory as a cowork directory
- **No new RPC methods** — Protocol remains at 22 methods and 8 event types
- **SDK versions unchanged** — Same Electron 40.8.5, claude-agent-sdk 0.2.92, claude-agent-sdk-future 0.2.93-dev, conway-client unchanged
- **cowork-svc.exe**: Minor rebuild (+1.5 KB), same Go version (go1.24.13), TLS certificate date rotation, no new handler functions

### New in v1.1348.0

- **Rebuild only** — No new features, tools, or protocol changes. Minified variable names changed (different build), IPC bridge UUID updated, same SDK versions and Electron version as v1.1062.0
- **Plugin MCP refresh** — Minor enhancement: plugin MCPs are refreshed after uninstall (`refreshPluginMcps()` call added)

### New in v1.1062.0

- **Cowork onboarding system** — New `cowork-onboarding` MCP server with `show_onboarding_role_picker` tool; `setup-cowork` skill providing guided onboarding flow (role picking, skill intro, connector intro). Gate-checked, scoped to `sessionType==="cowork"`
- **Cowork search subsystem** — New `[cowork-search]` worker using `transcriptSearchWorker.js`; new IPC methods `searchSessions(query, options)` on `LocalSessions` and `LocalAgentModeSessions`
- **Session file operations** — `readFileAtCwd`, `pickFileAtCwd`, `pickSessionFile`, `writeSessionFile` (with optional hash-based CAS)
- **Deploy/preview system** — `deployPreview`, `suggestDeployName`, `unpublishDeploy` IPC methods; `deployEvent` event dispatched to renderer
- **Marketplace enhancements** — `createAccountMarketplace`, `listAccountMarketplaces`, `uploadAccountPlugin`, `fetchOrgMarketplaceNames` IPC methods
- **Connectors concept** — New MCP tools: `suggest_connectors`, enhanced `search_mcp_registry`; part of onboarding flow
- **Transcript feedback** — `getTranscriptFeedback`, `submitTranscriptFeedback` IPC methods
- **Auto-fix feature** — `setAutoFixEnabled(sessionId, enabled)` per-session toggle; persisted in session state and GitHub PR config
- **Cowork egress blocking** — New `cowork-egress-blocked` constant for workspace MCP server; network egress control with IP range detection
- **Expanded disallowed tools** — Bridge/dispatch disallowed list now includes `show_onboarding_role_picker`; CU-only mode adds `mcp__mcp-registry__suggest_connectors`, `mcp__plugins__suggest_plugin_install`, and more
- **Removed settings** — `isClaudeCodeForDesktopEnabled`, `isDesktopExtensionEnabled`, `autoUpdaterEnforcementHours`, `setCiMonitorEnabled`, `forceLoginOrgUUID`, `customDeploymentUrl` removed
- **Binary changes** — cowork-svc.exe internal cert handling refactored (`enumerateRootStore`), new `vm/rpc_types.go` source file. No new RPC methods. Binary shrank 8KB
- **No pipe protocol changes** — All 22 RPC methods, 8 event types, spawn parameters, and wire format unchanged
- **SDK versions updated** — claude-agent-sdk 0.2.92 (was 0.2.87), claude-agent-sdk-future 0.2.93-dev (was 0.2.90-dev), conway-client updated

### Key Dependency Versions

*(verified for v1.2773.0)*

| Package | Version | Changed from v1.2581.0 |
|---------|---------|------------------------|
| @anthropic-ai/claude-agent-sdk | 0.2.92 | was 0.2.101 (rolled back) |
| @anthropic-ai/claude-agent-sdk-future | 0.2.93-dev.20260403 | was 0.2.102-dev.20260410 (rolled back) |
| @anthropic-ai/conway-client | 0.2.0-dev.20260403 | unchanged |
| @anthropic-ai/mcpb | 2.1.2 | — |
| @anthropic-ai/sdk | ^0.70.0 | — |
| @modelcontextprotocol/sdk | 1.28.0 | — |
| electron | 41.2.0 | unchanged |
| typescript | ~5.8.3 | — |
| zod | ^3.25.64 | — |
| ws | ^8.18.0 | — |
| ssh2 | ^1.16.0 | — |

### Internal Workspace Packages

@ant/chrome-native-host, @ant/claude-ssh, @ant/cowork-win32-service, @ant/claude-screen-app, @ant/claude-swift-ant, @ant/computer-use-mcp, @ant/imagine-server, @anthropic-ai/operon-core, @anthropic-ai/operon-web

---

## Key Reverse Engineering Findings

1. The Go binary uses standard library HTTP/JSON, making protocol analysis straightforward
2. The vsock port 0xC822 (51234) is hardcoded in both platforms
3. The named pipe on Windows uses the same length-prefixed JSON protocol as Unix sockets
4. cowork-svc.exe includes a bundle downloader that fetches VM images from the CDN on first use
5. The smol-bin.vhdx is used as a side-loaded disk for updating sdk-daemon inside the VM
6. Spawn parameters match exactly between Windows and macOS (same field names, same JSON structure)

## What to Check on Update

1. Run `strings bin/cowork-svc.exe | grep -i "method\|spawn\|subscribe\|event"` for new RPC methods
2. Check if new files appear at the same directory level
3. Compare binary size — significant changes may indicate new functionality
4. Check the app.asar for changes to the TypeScript VM client (session management code)
5. Compare cowork-svc.exe SHA256 against previous version
6. Check Go version: `strings bin/cowork-svc.exe | grep "^go[0-9]"`
7. Check for new `handle` functions: `strings bin/cowork-svc.exe | grep "handle[A-Z]"`
8. Check app.asar dependency versions (especially @anthropic-ai/* and @modelcontextprotocol/sdk)
9. Look for new internal workspace packages

## Version History

| Claude Desktop Version | cowork-svc.exe Size | Notable Changes |
|----------------------|-------------------|-----------------|
| 1.3109.0 | 12,648,272 bytes | Clean rebuild, byte-identical size; only build metadata changed; no new RPC methods; no new handlers; no new error strings; VM bundle unchanged; SDK versions unchanged; app.asar grew significantly but only minifier renames, all of our RPC methods and session dispatch logic unchanged |
| 1.3036.0 | 12,648,272 bytes | Rebuild +4 KB; new Windows cert store helpers (`enumerateCertStore`, `certChainsToTrustedRoot`); no new RPC methods; Desktop injects `ENABLE_PROMPT_CACHING_1H=1` env var; new `cowork-plugin-oauth` storage; new `cu_lock_released`/`cu_teach_session` telemetry; new `setup-cowork` skill; `cowork_lock_midsession_model` gate |
| 1.2773.0 | 12,644,176 bytes | Rebuild +512 bytes; SDK rolled back (0.2.92); no new RPC methods; cowork-deletion event logging; dispatchOnCliOpAlwaysAllowed; coworkWebSearchEnabled gate removed |
| 1.2581.0 | 12,643,664 bytes | Clean rebuild, same size; no new RPC methods; new `cowork-file` URL scheme for native file preview; `coworkNativeFilePreview` + `coworkKappa` feature flags; LibreOffice document conversion; permission routing split (cowork vs ccd); `getCodeStats` IPC method; plugin shim updated |
| 1.2278.0 | 12,643,664 bytes | +13.1% size; WPAD/PAC proxy auto-discovery (net/http, crypto/tls linked); no new RPC methods; Electron 41.2.0; SDK 0.2.101; new Desktop features: cowork.sample(), forkSession, rewind, vertexAuth, coworkWebFetchViaApi |
| 1.1617.0 | 11,179,344 bytes | Rebuild +1.5 KB; TLS cert rotation; no new RPC methods; new Desktop features: coworkEgressAllowedHosts, canUseTool VM path guard, plugin shim integration |
| 1.1348.0 | 11,177,808 bytes | Rebuild only — same size, same Go version, updated timestamps/VCS revision; no new RPC methods; SDK versions unchanged |
| 1.1062.0 | 11,177,808 bytes | Internal cert refactor (`enumerateRootStore`), new `vm/rpc_types.go`; no new RPC methods; SDK 0.2.92; binary shrank 8KB |
| 1.569.0 | 11,186,000 bytes | New RPC method `sendGuestResponse` (plugin permission bridge); binary grew ~11KB |
| 1.2.234 | 11,174,736 bytes | Rebuild only; Electron 40.8.5, dispatchCodeTasksPermissionMode, plugin permission bridge mounts |
| 1.1.9669 | 11,174,736 bytes | New: cowork-plugin-shim.sh, conda disk support, plugin system, coworkArtifact.js |
| 1.1.9493 | 11,162,448 bytes | Previous |
| 1.1.9310 | (check previous) | — |
| 1.1.7464 | (original extraction) | First reverse engineering |
| 1.1.4173 | (initial discovery) | Original README reference |
