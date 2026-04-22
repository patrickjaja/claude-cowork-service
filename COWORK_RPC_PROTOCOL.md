# Cowork RPC Protocol Reference — v1.3883.0

> **This document is the single source of truth for the protocol between Claude Desktop and cowork-svc.**
> Re-validate on every upstream Claude Desktop version update.

---

## Table of Contents

- [Wire Protocol](#wire-protocol)
- [RPC Methods (21 total)](#rpc-methods-21-total)
- [Event Types (9 total)](#event-types-9-total)
- [Protocol Discoveries](#protocol-discoveries)
- [Linux-Specific Adaptations](#linux-specific-adaptations)
- [Session Types](#session-types)

---

## Wire Protocol

**Transport:** Unix domain socket at `$XDG_RUNTIME_DIR/cowork-vm-service.sock`
(fallback: `/tmp/cowork-vm-service.sock` when `$XDG_RUNTIME_DIR` is unset)

**Framing:** 4-byte big-endian length prefix + JSON payload

**Max message size:** 10 MB (10,485,760 bytes). Messages exceeding this limit are rejected.

**Socket permissions:** `0700` (owner read/write/execute only)

**Connection model:** Claude Desktop opens multiple concurrent connections to the socket. Each connection handles one request/response at a time, except for `subscribeEvents` which holds the connection open for streaming.

### Request Format

```json
{"method": "string", "params": {...}, "id": number|string}
```

- `method` (string, required): The RPC method name.
- `params` (object, optional): Method-specific parameters.
- `id` (number or string, required): Request identifier echoed back in the response.

### Response Format

**Success:**
```json
{"id": ..., "success": true, "result": {...}}
```

**Error:**
```json
{"id": ..., "success": false, "error": "message"}
```

The `id` field MUST echo back the request ID so the client can match responses to requests. Without it, responses are treated as orphaned.

### Error Codes

| Code | Meaning |
|------|---------|
| `-32700` | Parse error (invalid JSON) |
| `-32602` | Invalid params (missing or malformed parameters) |
| `-32000` | Backend error (operation failed) |

### Unknown Methods

Unknown method names receive a success response with `null` result (passthrough behavior). This ensures forward compatibility when Desktop sends methods the daemon does not yet implement.

---

## RPC Methods (22 total)

### 1. `configure`

Accepts VM resource configuration. On native Linux, values are logged but ignored since there is no VM to configure.

**Params:**
```json
{
  "memoryMB": int,
  "cpuCount": int
}
```

**Response:** `null`

**Native Linux behavior:** Stores the values internally but takes no action. Logged in debug mode.

**Notes:** Called early in session setup, before `createVM`.

---

### 2. `createVM`

Creates a VM instance (or session directory on native Linux).

**Params:**
```json
{
  "name": string,
  "bundlePath": string,
  "diskSizeGB": int
}
```

**Response:** `null`

**Native Linux behavior:** No-op. The name from `bundlePath` is extracted as a fallback if `name` is empty (`filepath.Base(bundlePath)`).

**Notes:** The `name` is used as the session directory identifier throughout the session lifecycle.

---

### 3. `startVM`

Starts the VM (or marks the native backend as started and emits startup events).

**Params:**
```json
{
  "name": string,
  "bundlePath": string,
  "memoryGB": int
}
```

**Response:** `null`

**Native Linux behavior:** Sets `started=true`, then emits the following events asynchronously after a 500ms delay:

1. `startupStep` with step `"CERTIFICATE"`
2. `vmStarted` with the VM name
3. `startupStep` with step `"VirtualDiskAttachments"`
4. `apiReachability` with `reachability: "reachable"`

**Notes:** The 500ms delay is critical -- it prevents a race condition where events are emitted before `subscribeEvents` has connected (both calls arrive simultaneously on different connections). See Discovery #5.

---

### 4. `stopVM`

Stops the VM (or kills all tracked processes on native Linux).

**Params:**
```json
{
  "name": string
}
```

**Response:** `null`

**Native Linux behavior:** Sets `started=false`, kills all tracked processes (entire process groups), emits `vmStopped` event.

**Notes:** Claude Desktop calls `stopVM` as cleanup before starting new sessions too, not just at shutdown.

---

### 5. `isRunning`

Checks if the VM is running.

**Params:**
```json
{
  "name": string
}
```

**Response:**
```json
{
  "running": boolean
}
```

**Native Linux behavior:** Returns the `started` flag set by `startVM`/`stopVM`.

---

### 6. `isGuestConnected`

Checks if the guest agent inside the VM is connected.

**Params:**
```json
{
  "name": string
}
```

**Response:**
```json
{
  "connected": boolean
}
```

**Native Linux behavior:** Always returns `true`. On native Linux there is no VM boundary -- the host IS the guest. Returning `false` causes Claude Desktop to poll repeatedly until timeout.

**Notes:** Claude Desktop calls this before `startVM` to check if it can skip boot.

---

### 7. `spawn`

Spawns a command as a child process. This is the most complex method in the protocol.

**Params:**
```json
{
  "name": string,
  "id": string,
  "command": string,
  "args": [string],
  "env": {string: string},
  "cwd": string,
  "additionalMounts": {
    "mount-name": {
      "path": string,
      "mode": string
    }
  },
  "isResume": boolean,
  "allowedDomains": [string],
  "oneShot": boolean,
  "mountSkeletonHome": boolean,
  "mountConda": string
}
```

**New fields (v1.1.9669):**
- `isResume` (boolean, default `false`): Whether this is a resumed session.
- `allowedDomains` (array of strings, optional): Network egress allowlist for the spawned process. Ignored on native Linux (no network isolation).
- `oneShot` (boolean, default `false`): For one-shot command execution.
- `mountSkeletonHome` (boolean, default `false`): Whether to mount a skeleton home directory.
- `mountConda` (string, optional): Conda environment mount mode — `"ro"`, `"rw"`, or `"rwd"`. Ignored on native Linux.

**New additionalMounts entries (v1.2.234):**
- `.cowork-lib` (mode `"ro"`): Plugin shim library directory. Contains `shim.sh` sourced by plugin shims for token validation, permission gating, and arch-aware binary dispatch.
- `.cowork-perm-req` (mode `"rw"`): Permission bridge request directory. Plugin shims write JSON request files here to trigger user confirmation prompts.
- `.cowork-perm-resp` (mode `"ro"`): Permission bridge response directory. Host writes `"allow"` or `"deny"` responses here after user interaction.

These mounts are only present when plugins are configured for the session. On native Linux, they are processed by the existing mount symlink handler.

**Response:**
```json
{
  "id": "process-id"
}
```

**IMPORTANT:** The command field is `"command"`, NOT `"cmd"`. The process ID field in params is `"id"`, NOT `"processId"`. See Discoveries #1 and #2.

**Native Linux behavior (step by step):**

1. **Session directory creation:** Creates `~/.local/share/claude-cowork/sessions/<name>/mnt/`
2. **Mount symlink creation:** For each entry in `additionalMounts`, creates a symlink from `mnt/<mount-name>` to the real host path. Skips non-directory mounts (e.g., `app.asar`). Detects and skips self-referencing symlinks (ELOOP prevention).
3. **Root symlink attempt:** Tries to create `/sessions/<name>` pointing to the real session dir (may fail without root -- that is OK).
4. **Path remapping:** Remaps `cwd`, `env` values, and `args` that contain `/sessions/<name>` to the real session directory path.
5. **CWD selection:** Uses the first non-hidden, non-special (`uploads`, `outputs`) mount as the working directory, resolving to the real filesystem path (not the symlink) so Glob works correctly.
6. **Environment cleanup:** Strips all empty-string environment variables (prevents auth failures -- see Discovery #8).
7. **MCP server pass-through:** SDK MCP servers (`dispatch`, `cowork`, `session_info`) in `--mcp-config` are kept intact. The CLI sends `control_request` messages via the event stream, and Desktop's session manager handles them natively.
8. **`--disallowedTools` stripping:** Removes the entire `--disallowedTools` flag and its value. Desktop passes this for VM sessions where certain tools are handled by the VM runtime. On native Linux, all tools must be available to the CLI directly.
9. **`--brief` flag injection:** When `CLAUDE_CODE_BRIEF=1` is in the environment (dispatch/agent sessions), injects `--brief` into args if not already present. This ensures the CLI registers `SendUserMessage` in its tool list.
10. **Mount path remapping (forward):** Builds forward mappings: `session/mnt/<mount>` to real host path (for stdin data).
11. **Mount path remapping (reverse):** Builds reverse mappings: real host path to VM-style `/sessions/<name>/mnt/<mount>` (for stdout data sent to Desktop).
12. **Binary resolution (3-stage fallback):**
    - Stage 1: `exec.LookPath` -- checks current process PATH
    - Stage 2: `bash -lc "which <binary>"` -- login shell, loads `~/.bash_profile` / `~/.profile`
    - Stage 3: `$SHELL -lic "command -v <binary>"` -- interactive login shell, loads `~/.bashrc` (PATH additions behind interactive guards)
13. **Process group creation:** Sets `Setpgid: true` so the entire process group can be killed.
14. **Nested session prevention:** Strips `CLAUDECODE` and `CLAUDE_CODE_ENTRYPOINT` environment variables to prevent "cannot be launched inside another Claude Code session" errors.
15. **Output streaming:** Both stdout and stderr are streamed, with stderr content emitted as `stdout` events (Claude Code writes stream-json on stderr -- see Discovery #10).

---

### 8. `kill`

Sends a signal to a spawned process.

**Params:**
```json
{
  "id": string,
  "signal": string
}
```

**Response:** `null`

**Signal mapping:**

| Signal string | Mapped to |
|--------------|-----------|
| `"KILL"` | `SIGKILL` |
| `"INT"` | `SIGINT` |
| `"QUIT"` | `SIGQUIT` |
| `"HUP"` | `SIGHUP` |
| `"USR1"` | `SIGUSR1` |
| `"USR2"` | `SIGUSR2` |
| `""` or unrecognized | `SIGTERM` |

Signal strings are case-insensitive and the `SIG` prefix is stripped if present (e.g., `"SIGKILL"` and `"KILL"` are equivalent).

**Notes:**
- **1-second delay before kill** to let pending result events propagate to the renderer. The Electron app sends kill immediately after receiving the result event, before the UI has time to render the response. This is especially visible in Dispatch where the result never appears in the UI.
- Kills the **entire process group** (via negative PGID), not just the process itself.

---

### 9. `writeStdin`

Writes data to a spawned process's stdin pipe.

**Params:**
```json
{
  "id": string,
  "data": string
}
```

**Response:** `null`

**Native Linux behavior:**
1. **Forward path remapping:** VM paths (`/sessions/<name>/...`) are remapped to real host paths in the data before writing.
2. **Forward mount remapping:** Session mount paths are remapped to real mount target paths (Glob does not follow directory symlinks).
3. **MCP `control_response` logging:** Detects and logs `control_response` messages from Desktop's MCP proxy.
4. **`sdkMcpServers` detection:** Logs `initialize` messages containing SDK MCP server configuration.
5. **Skill prefix stripping:** Strips plugin prefixes from skill invocations in user messages. The Cowork UI sends `/<plugin>:<skill>` but the CLI expects `/<skill>` (bare skill name). Regex: `"content":"/[a-zA-Z0-9_-]+:` is replaced with `"content":"/`.
6. **Process alive check:** Returns error if the process has already exited.
7. **10-second write timeout:** Prevents blocking forever if the stdin buffer is full. Also checks for process exit during the write.

---

### 10. `isProcessRunning`

Checks if a specific spawned process is still running.

**Params:**
```json
{
  "id": string
}
```

**Response:**
```json
{
  "running": boolean
}
```

**Notes:** Returns `false` for unknown process IDs (no error).

---

### 11. `mountPath`

Mounts a path for a running process in the VM guest filesystem.

**Params:**
```json
{
  "processId": string,
  "subpath": string,
  "mountName": string,
  "mode": string
}
```

**Response:** `null`

**Native Linux behavior:** No-op. Paths are already native -- no mounting needed. Logged in debug mode.

**Note (v1.3883.0 fix):** Desktop has always sent `{processId, subpath, mountName, mode}` — the previous documentation was incorrect.

---

### 12. `readFile`

Reads a file from the VM filesystem (or directly from the host on native Linux).

**Params:**
```json
{
  "processName": string,
  "filePath": string
}
```

**Response:**
```json
{
  "content": "file-contents-as-string"
}
```

**Native Linux behavior:** Direct `os.ReadFile()` on the given path.

**Note (v1.3883.0 fix):** Desktop has always sent `{processName, filePath}` and expected `{content}` in the response — the previous documentation and handler code were incorrect.

---

### 13. `installSdk`

Installs the SDK inside the VM guest.

**Params:**
```json
{
  "name": string
}
```

**Response:** `null`

**Native Linux behavior:** No-op. The SDK (Claude Code CLI) is already installed on the host.

---

### 14. `addApprovedOauthToken`

Stores an approved OAuth token for use by spawned processes.

**Params:**
```json
{
  "name": string,
  "token": string
}
```

**Response:** `null`

**Native Linux behavior:** No-op. Logged in debug mode.

---

### 15. `setDebugLogging`

Toggles verbose debug logging at runtime.

**Params:**
```json
{
  "enabled": boolean
}
```

**Response:** `null`

**Notes:** Affects both the backend and the process tracker logging.

---

### 16. `isDebugLoggingEnabled`

Queries the current debug logging state.

**Params:** none

**Response:**
```json
{
  "enabled": boolean
}
```

---

### 17. `subscribeEvents`

Subscribes to the event stream for a VM/session. This is a long-lived connection.

**Params:**
```json
{
  "name": string
}
```

**Response (initial acknowledgment):**
```json
{
  "subscribed": true
}
```

**Streaming behavior:** After the initial acknowledgment, events are sent as length-prefixed JSON messages on the same connection. The connection blocks (reading for incoming messages) until the client disconnects.

**Concurrency:** An atomic flag (`cancelled`) prevents event writes after a connection write failure. A mutex serializes concurrent event writes on the same connection.

**Notes:** When the connection drops, `ReadMessage` fails and the subscription is cancelled. Events are pushed via a callback function registered with the backend.

---

### 18. `getDownloadStatus`

Returns the download/readiness status of the VM bundle.

**Params:** none

**Response (native Linux):**
```json
{
  "status": "ready"
}
```

**Response (VM mode, no bundle):**
```json
{
  "status": "NotDownloaded"
}
```

**Native Linux behavior:** Always returns `"ready"` since there is no VM bundle to download.

---

### 19. `getSessionsDiskInfo`

Returns disk usage information for session directories. Used by Desktop's `VMDiskJanitor` to manage disk space.

**Params:**
```json
{
  "lowWaterBytes": int
}
```

**Response:**
```json
{
  "totalBytes": int,
  "freeBytes": int,
  "sessions": []
}
```

**Native Linux behavior:** Returns zeros and an empty sessions list. Native Linux uses the host filesystem directly — no virtual disks to manage.

**Added in:** v1.1.9669

---

### 20. `deleteSessionDirs`

Deletes session directories to free disk space. Called by Desktop's janitor when disk space is low.

**Params:**
```json
{
  "names": [string]
}
```

**Response:**
```json
{
  "deleted": [string],
  "errors": {}
}
```

**Native Linux behavior:** No-op. Returns empty deleted list and empty errors. Session dirs on native Linux are managed directly on the host filesystem.

**Added in:** v1.1.9669

---

### 21. `createDiskImage`

Creates a virtual disk image (e.g., for conda environments). Used to create a 50GB `condadata.vhdx/img` for conda package management inside the VM.

**Params:**
```json
{
  "diskName": string,
  "sizeGiB": int
}
```

**Response:** `null`

**Native Linux behavior:** No-op. Native Linux doesn't need virtual disk images — conda runs directly on the host filesystem.

**Added in:** v1.1.9669

---

### 22. `sendGuestResponse`

Delivers a host response back to a VM guest process. Used by the plugin permission bridge: when a plugin shim in the VM requests permission for a sensitive operation, the host shows a permission prompt and sends the result back via this method.

**Params:**
```json
{
  "id": string,
  "resultJson": string,
  "error": string
}
```

- `id`: The guest request ID to respond to.
- `resultJson`: JSON-encoded result payload (empty string if error).
- `error`: Error message (empty string on success).

**Response:** `null`

**Native Linux behavior:** No-op. On native Linux, the plugin permission bridge uses the filesystem directly (shims write to `.cowork-perm-req/`, host writes responses to `.cowork-perm-resp/`). In VM mode, this RPC delivers responses over vsock.

**Added in:** v1.569.0

---

## Event Types (9 total)

Events are sent over the `subscribeEvents` connection as length-prefixed JSON messages (same framing as RPC responses, but without `success`/`id` fields).

### 1. `stdout`

Emitted when a process writes to stdout (or stderr -- see Discovery #10).

```json
{
  "type": "stdout",
  "id": "process-id",
  "data": "line of output\n"
}
```

**Notes:** The `id` field is the process identifier, NOT `"processId"` (see Discovery #12). Data includes the trailing newline. On native Linux, both stdout and stderr from spawned processes are emitted as `stdout` events because Claude Code writes its stream-json output on stderr.

### 2. `stderr`

Emitted when a process writes to stderr (used in VM mode; on native Linux, stderr is redirected to stdout events).

```json
{
  "type": "stderr",
  "id": "process-id",
  "data": "error output"
}
```

### 3. `exit`

Emitted when a process exits.

```json
{
  "type": "exit",
  "id": "process-id",
  "exitCode": 0,
  "signal": "SIGTERM",
  "oomKillCount": 0
}
```

**Fields:**
- `exitCode` (int): The process exit code. `-1` for non-ExitError failures.
- `signal` (string, optional): Present only when the process was killed by a signal (e.g., `"SIGTERM"`, `"SIGKILL"`).
- `oomKillCount` (int, optional): OOM kill count. Always `0` on native Linux.

### 4. `apiReachability`

Indicates whether the API is reachable from the execution environment.

```json
{
  "type": "apiReachability",
  "status": "reachable"
}
```

**`status` values:** `"unknown"`, `"reachable"`, `"probably_unreachable"`, `"unreachable"`

**Notes:** Claude Desktop requires this event during startup. Without it, the client remains stuck waiting for the environment to become ready (Discovery #6). Desktop reads `s.status` from the event (not `reachability` — corrected in v1.2581.0 update).

### 5. `error`

Emitted for process-level errors.

```json
{
  "type": "error",
  "id": "process-id",
  "message": "error description",
  "fatal": false
}
```

**Fields:**
- `fatal` (boolean): If `true`, the error is unrecoverable (e.g., process failed to start).

### 6. `startupStep`

Reports VM/environment startup progress.

```json
{
  "type": "startupStep",
  "step": "CERTIFICATE",
  "status": "completed"
}
```

**Known step values:** `"CERTIFICATE"`, `"VirtualDiskAttachments"`

**`status` values:** `"started"` (step beginning), `"completed"` (step finished). Desktop guards with `s.step && s.status` — both fields must be truthy. Status `"started"` triggers `stepStarted()`; any other value triggers `stepCompleted()`.

### 7. `vmStarted`

Emitted when the VM (or native backend) has started.

```json
{
  "type": "vmStarted",
  "name": "vm-name"
}
```

### 8. `vmStopped`

Emitted when the VM (or native backend) has stopped.

```json
{
  "type": "vmStopped",
  "name": "vm-name"
}
```

### 9. `networkStatus`

Reports network connectivity state. Desktop uses this to detect when the VM has network access.

```json
{
  "type": "networkStatus",
  "status": "CONNECTED"
}
```

**`status` values:** `"CONNECTED"`, `"NOT_CONNECTED"`

**Notes:** On native Linux, emitted as `"CONNECTED"` during startup since the host has direct network access. Desktop starts a connection timeout timer on `"NOT_CONNECTED"` and clears it on `"CONNECTED"`.

---

## Protocol Discoveries

During reverse engineering, 12 mismatches were found between the documented/expected protocol and what Claude Desktop actually sends. These are critical for anyone building a compatible implementation.

### Discovery #1: Spawn field is `"command"` not `"cmd"`

- **Symptom:** Empty command string, process killed immediately.
- **Fix:** Changed the JSON tag on the spawn params struct from `"cmd"` to `"command"`.
- **Source reference:** `spawnParams.Cmd` has tag `` `json:"command"` `` in `pipe/handlers.go`.

### Discovery #2: Process ID field is `"id"` not `"processId"` in RPC params

- **Symptom:** `writeStdin` data never reached the process.
- **Fix:** Changed JSON tags on `killParams`, `processIDParams`, and `writeStdinParams` from `"processId"` to `"id"`.

### Discovery #3: Binary path `/usr/local/bin/claude` does not exist on host

- **Symptom:** `exec.Command` failed -- the VM-expected path does not exist on the host.
- **Fix:** 3-stage binary resolution fallback: `exec.LookPath` -> `bash -lc which` -> `$SHELL -lic command -v`.

### Discovery #4: `/sessions/<name>` requires root to create

- **Symptom:** `mkdir` failed when running as a non-root user.
- **Fix:** Remap all session paths to `~/.local/share/claude-cowork/sessions/`. Attempt root symlink but gracefully handle failure.

### Discovery #5: `subscribeEvents` races with `startVM`

- **Symptom:** Startup events lost, client stuck waiting forever.
- **Fix:** Delay event emission by 500ms in `startVM` to give `subscribeEvents` time to connect.

### Discovery #6: Client needs `apiReachability` event (not just `isGuestConnected`)

- **Symptom:** Client stuck after boot, never proceeds to spawn.
- **Fix:** Emit `apiReachability` with `status: "reachable"` during `startVM` event sequence.

### Discovery #7: Args also contain VM paths (not just cwd/env)

- **Symptom:** `--plugin-dir /sessions/...` unresolvable on host.
- **Fix:** Remap args that start with `/sessions/<name>` to the real session directory path.

### Discovery #8: Empty env vars (`ANTHROPIC_API_KEY=""`) break auth

- **Symptom:** Valid OAuth token ignored; CLI uses empty API key instead.
- **Fix:** Strip all empty-string environment variables before spawning the process.

### Discovery #9: `sdkMcpServers` in MCP config (RESOLVED)

- **Symptom:** Process hangs at init -- zero output.
- **Original fix:** Strip SDK servers from config.
- **Resolution:** Was actually caused by other bugs (#3, #4, #8). SDK servers now pass through and work via the event-stream MCP proxy. Desktop's session manager handles `control_request`/`control_response` over the event stream natively, identical to VM mode.

### Discovery #10: Claude Code outputs stream-json on stderr, not stdout

- **Symptom:** Captured stdout was empty; no output reached Desktop.
- **Fix:** Emit stderr content as `stdout` events. Both pipes are read and all output is sent as type `"stdout"`.

### Discovery #11: MCP proxy requests (RESOLVED)

- **Symptom:** Process hangs mid-conversation when MCP tools are called.
- **Original fix:** Auto-respond with error.
- **Resolution:** Desktop's session manager handles `control_request`/`control_response` over the event stream natively. No auto-responding needed.

### Discovery #12: Event field is `"id"` not `"processId"`

- **Symptom:** Events ignored by Desktop, UI stuck on "Starting up...".
- **Fix:** Changed event struct JSON tags from `"processId"` to `"id"`.
- **Source reference:** All event structs in `process/events.go` use `` `json:"id"` ``.

---

## Linux-Specific Adaptations

These adaptations are applied in `native/backend.go` and `native/process.go` to make the protocol work without a VM layer.

### 1. `--disallowedTools` stripping

Desktop passes `--disallowedTools` containing tools handled by the VM runtime:
`AskUserQuestion`, `mcp__cowork__allow_cowork_file_delete`, `mcp__cowork__present_files`, `mcp__cowork__launch_code_session`, `mcp__cowork__create_artifact`, `mcp__cowork__update_artifact`.

On native Linux there is no VM runtime, so the entire flag and its value are removed -- all tools are available to the CLI directly.

**Expanded in v1.1062.0** — bridge/dispatch disallowed list now also includes `mcp__cowork-onboarding__show_onboarding_role_picker`. CU-only mode adds: `mcp__workspace__bash`, `mcp__workspace__web_fetch`, `mcp__cowork__launch_code_session`, `mcp__cowork__present_files`, `mcp__cowork__request_cowork_directory`, `mcp__cowork__allow_cowork_file_delete`, `mcp__mcp-registry__search_mcp_registry`, `mcp__mcp-registry__suggest_connectors`, `mcp__plugins__search_plugins`, `mcp__plugins__suggest_plugin_install`. New built-in disallowed: `Bash`, `NotebookEdit`, `REPL`, `JavaScript`, `WebFetch`.

### 2. `--brief` flag injection

Desktop passes `CLAUDE_CODE_BRIEF=1` in the environment for Ditto/dispatch agent sessions only (not for regular cowork). The backend detects this and injects the `--brief` CLI flag, which ensures the CLI registers `SendUserMessage` in its tool list. This was broken in CLI v2.1.79-2.1.85, fixed in v2.1.86.

### 3. `present_files` interception

Desktop's built-in `present_files` MCP handler validates file paths against VM-style mounts and rejects native Linux paths ("not accessible on user's computer"). The backend intercepts `present_files` `control_request` messages in `streamOutput`, verifies the files exist on disk, and returns a synthetic success response directly to the CLI's stdin -- bypassing Desktop entirely.

The response includes a hint for the model to use `SendUserMessage` with `attachments` for phone delivery, since `present_files` UI cards only appear in the Desktop app.

### 4. Reverse mount path mapping

The backend builds reverse mount remappings (real host path to VM-style `/sessions/<name>/mnt/<mount>`) applied to outgoing `control_request` messages. This ensures tools other than `present_files` that flow through Desktop's MCP proxy can resolve paths correctly.

### 5. Skill prefix stripping

The Cowork UI sends `/<plugin>:<skill>` (e.g., `/document-skills:pdf`) but the CLI expects `/<skill>` (e.g., `/pdf`). The plugin prefix from `marketplace.json` does not match the CLI's `plugin.json` name, so the backend strips it via regex on stdin data.

### 6. Nested session prevention

Strips `CLAUDECODE` and `CLAUDE_CODE_ENTRYPOINT` environment variables from spawned processes. When `cowork-svc` is started from within a Claude Code session, these inherited vars cause spawned CLI instances to refuse to start with "cannot be launched inside another Claude Code session".

### 7. Binary resolution 3-stage fallback

Systemd services have minimal PATH (`/usr/local/bin:/usr/bin`), so the daemon uses multi-stage shell-based fallbacks to locate binaries installed in user-specific locations:

| Stage | Command | What it loads |
|-------|---------|---------------|
| 1 | `exec.LookPath(base)` | Current process PATH |
| 2 | `bash -lc "which <base>"` | `~/.bash_profile`, `~/.profile` |
| 3 | `$SHELL -lic "command -v <base>"` | `~/.bashrc` (PATH additions behind interactive guards) |

Stage 3 parses all output lines for an absolute path that exists on disk, filtering out shell init noise (fastfetch, motd, etc.).

### 8. Empty env var stripping

All environment variables with empty string values are deleted before spawning. Empty `ANTHROPIC_API_KEY=""` overrides valid OAuth credentials, breaking authentication.

---

## Session Types

Claude Desktop uses three session types, identified by the `CLAUDE_CODE_TAGS` environment variable passed to spawned processes:

| Type | `CLAUDE_CODE_TAGS` | `CLAUDE_CODE_BRIEF` | `SendUserMessage` tool | Dispatch MCP servers |
|------|-------------------|---------------------|----------------------|---------------------|
| Regular cowork | `lam_session_type:chat` | *(not set)* | No | No |
| Ditto orchestrator | `lam_session_type:agent` | `1` | **Yes** | **Yes** |
| Dispatch child | `lam_session_type:dispatch_child` | *(not set)* | No | No |

**New in v1.2.234:** `dispatchCodeTasksPermissionMode` preference controls the permission mode for dispatch code tasks (`"default"`, `"acceptEdits"`, `"plan"`, `"auto"`, `"bypassPermissions"`). This is a Desktop UI setting — it does not affect the RPC protocol.

**New dispatch tools (v1.2.234):** `start_code_task` (MCP tool) — specialized dispatch tool for code-related work. Desktop prefers this over `start_task` for editing repos, running tests, etc. This is handled by Desktop's MCP proxy, not by cowork-svc.

### Regular Cowork (`chat`)

Standard Cowork sessions initiated from the Desktop UI. The user types a message and a Claude Code CLI session handles it. No dispatch tools, no brief mode.

### Ditto Orchestrator (`agent`)

The long-running dispatch orchestrator agent (internally called "Ditto", visible in session directories as `local_ditto_*`). This agent:
- Receives messages from the phone via SSE
- Has `SendUserMessage` tool for replying to the phone
- Has dispatch MCP tools (`mcp__dispatch__start_task`, etc.) for delegating work to child sessions
- Runs in brief mode (`--brief` flag injected by the backend)

### Dispatch Child (`dispatch_child`)

Child sessions spawned by the Ditto orchestrator to do actual work (coding, file operations, research). These complete and their transcripts are read by Ditto, who then replies to the phone.

---

## Upstream Methods Not Yet Implemented

These methods exist in cowork-svc.exe (from binary string analysis) but are not in our handler:

| Method | Purpose | Risk |
|--------|---------|------|
| `handlePassthrough` | Forwards arbitrary requests to VM | Low — we handle all methods directly |
| `handlePersistentRPC` | Long-lived bidirectional RPC | Medium — may be used for future streaming features |
| `SetCondaDiskPath` | Conda environment management | Low — native Linux uses host conda directly |
| `InitSignatureVerification` / `verifyClientSignature` | Windows code signing verification | N/A — Linux doesn't use Windows code signing |
| `GetClientInfo` / `GetClientInfoFromConn` | Caller authentication | N/A — we trust all connections on the Unix socket |

**Note:** New methods in the binary don't necessarily mean new RPC protocol methods — some are internal Go functions. Monitor `handleX` patterns specifically.

**Newly implemented in v1.1.9669:** `createDiskImage`, `getSessionsDiskInfo`, `deleteSessionDirs` (all no-ops on native Linux).

**v1.2.234:** No new RPC methods. Protocol remains at 21 methods and 8 event types.

**v1.569.0:** New RPC method `sendGuestResponse` (22 methods, 8 event types). Used for the plugin permission bridge to deliver host responses back to VM guest processes.

**v1.1062.0:** No new RPC methods. Protocol remains at 22 methods and 8 event types. All changes are in the Electron app layer (onboarding, search, deploy, marketplace, connectors, auto-fix, egress blocking).

**v1.1348.0:** No new RPC methods. Protocol remains at 22 methods and 8 event types. Rebuild only — same binary size, same Go version, updated timestamps.

**v1.1617.0:** No new RPC methods. Protocol remains at 22 methods and 8 event types. Desktop-side additions: `coworkEgressAllowedHosts` admin egress allowlist (merges into existing `allowedDomains` spawn param), `canUseTool` VM path guard, `cowork-plugin-shim.sh` session integration, `request_cowork_directory` storage guard. All are Desktop-side enforcement — no wire protocol changes.

**v1.2278.0:** No new RPC methods. Protocol remains at 22 methods and 8 event types. cowork-svc.exe grew 13.1% from WPAD/PAC proxy auto-discovery (VM-internal only). All changes are Desktop-side: new IPC handlers (`forkSession`, `rewind`, `summarizeTranscript`, `vertexAuth`, `setBundledSkills`, `cowork.sample()`), new feature flags (`coworkWebFetchViaApi`, `vmEgressPolicy`), Electron 41.2.0, SDK 0.2.101. Wire protocol unchanged.

**v1.2773.0:** No new RPC methods. Protocol remains at 22 methods and 8 event types. cowork-svc.exe minor rebuild (+512 bytes). SDK versions rolled back. All changes Desktop-side: `[cowork-deletion]` event logging, `dispatchOnCliOpAlwaysAllowed`, `coworkWebSearchEnabled` gate removed. Wire protocol unchanged.

**v1.3036.0:** No new RPC methods. Protocol remains at 22 methods and 8 event types. cowork-svc.exe gained host CA cert store enumeration on Windows (`enumerateCertStore`, `certChainsToTrustedRoot`) — Windows-only, no wire impact. Desktop-side: new spawn env var `ENABLE_PROMPT_CACHING_1H=1` (passed through transparently), new `cowork-plugin-oauth` local storage, new `cu_lock_released` / `cu_teach_session` / `lam_mcp_servers_setup_summary` telemetry, new `setup-cowork` skill, new `cowork_lock_midsession_model` gate. Wire protocol unchanged.

**v1.3109.0:** No new RPC methods. Protocol remains at 22 methods and 8 event types. cowork-svc.exe is a **clean rebuild with byte-identical size** (12,648,272 bytes) — only build metadata differs (new VCS revision `35cbf6530e05912137624cde0f075dc7f121fa60`, timestamp `2026-04-16T20:32:01Z`). No new handler functions or error strings. app.asar grew substantially (10.1 → 14.6 MB) but is entirely minifier symbol renames; all 22 of our RPC method names are still called by Desktop, and all session dispatch machinery (`CLAUDE_CODE_TAGS:\`lam_session_type:${sessionType}\``, `CLAUDE_CODE_BRIEF`, `disallowedTools`, `present_files`, `session_type:"cowork"`) is unchanged. VM bundle unchanged (same SHA `5680b11b...`, same checksums). SDK versions unchanged. **No Go code changes required.**

---

## Session Lifecycle Sequence

A typical Cowork session follows this sequence:

```
Desktop                          cowork-svc
   │                                  │
   ├── stopVM(name) ────────────────►│  (cleanup from previous session)
   │                                  │
   ├── subscribeEvents(name) ───────►│  (opens long-lived event connection)
   │◄─── {subscribed: true} ─────────┤
   │                                  │
   ├── startVM(name) ───────────────►│
   │◄─── {success: true} ────────────┤
   │                                  │  (500ms delay)
   │◄─── startupStep CERTIFICATE ────┤  (started + completed)
   │◄─── vmStarted {name} ───────────┤
   │◄─── startupStep VirtualDisk... ──┤  (started + completed)
   │◄─── networkStatus CONNECTED ─────┤
   │◄─── apiReachability reachable ───┤
   │                                  │
   ├── spawn(command, args, env) ───►│
   │◄─── {id: "process-id"} ─────────┤
   │                                  │
   ├── writeStdin(id, initialize) ──►│  (MCP initialize with sdkMcpServers)
   ├── writeStdin(id, user msg) ────►│
   │                                  │
   │◄─── stdout event (stream-json) ──┤  (repeated, streaming response)
   │◄─── stdout event (stream-json) ──┤
   │                                  │
   │  (MCP tool call by CLI)          │
   │◄─── stdout control_request ──────┤  (SDK MCP tool invocation)
   ├── writeStdin(control_response) ►│  (Desktop proxies the result back)
   │                                  │
   │◄─── exit event ──────────────────┤
   │                                  │
   ├── kill(id, signal) ────────────►│  (1s delay, then kills process group)
   │                                  │
   ├── stopVM(name) ────────────────►│
   │◄─── vmStopped event ────────────┤
   │                                  │
```
