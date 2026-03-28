# claude-cowork-service

Native Linux backend for Claude Desktop's **Cowork** feature. Reverse-engineered from Windows [`cowork-svc.exe`](https://github.com/anthropics/cowork-win32-service) bundled with Claude Desktop v1.1.4173.

## What This Is

Claude Desktop has a Cowork feature that lets you delegate tasks to a sandboxed Claude Code instance. On macOS it uses Apple's Virtualization framework (Swift), on Windows it uses Hyper-V (Go). On Linux — there's no official support.

This daemon fills that gap. It implements the same length-prefixed JSON-over-Unix-socket protocol that Claude Desktop expects, but instead of managing a VM, it runs commands directly on the host.

**Key insight:** The VM on macOS/Windows runs Linux anyway. On Linux, we skip the VM and execute natively — because we're already the target OS.

## Installation

### Debian / Ubuntu (APT Repository)

```bash
# Add repository (one-time setup)
curl -fsSL https://patrickjaja.github.io/claude-cowork-service/install.sh | sudo bash

# Install
sudo apt install claude-cowork-service
```

Updates are automatic via `sudo apt update && sudo apt upgrade`.

### Fedora / RHEL (DNF Repository)

```bash
# Add repository (one-time setup)
curl -fsSL https://patrickjaja.github.io/claude-cowork-service/install-rpm.sh | sudo bash

# Install
sudo dnf install claude-cowork-service
```

Updates are automatic via `sudo dnf upgrade`.

<details>
<summary>Manual .rpm install (without DNF repo)</summary>

```bash
sudo dnf install https://github.com/patrickjaja/claude-cowork-service/releases/latest/download/claude-cowork-service-latest.x86_64.rpm
```
</details>

### Arch Linux (AUR)

```bash
yay -S claude-cowork-service
```

Updates arrive through your AUR helper (e.g. `yay -Syu`).

### NixOS

```nix
# In your flake.nix:
{
  inputs.claude-cowork-service.url = "github:patrickjaja/claude-cowork-service";

  # In your NixOS configuration:
  imports = [ inputs.claude-cowork-service.nixosModules.default ];
  services.claude-cowork.enable = true;
}
```

> **Note:** The cowork service invokes `claude` internally, which must be in the
> systemd service PATH. Systemd user services don't inherit your shell's PATH,
> so you need to declare it explicitly via `extraPath`:
>
> ```nix
> # Claude Code installed via npm global:
> services.claude-cowork.extraPath = [ pkgs.nodejs "/home/user/.npm-global" ];
>
> # Claude Code available as a Nix package:
> services.claude-cowork.extraPath = [ pkgs.claude-code ];
> ```

Or run directly with Nix:
```bash
nix run github:patrickjaja/claude-cowork-service
```

> **Note:** Update by running `nix flake update` to pull the latest version. `nix run` always fetches the latest.

### ARM64 / aarch64 (NVIDIA DGX Spark, Jetson, etc.)

ARM64 packages are available via the same APT and DNF repos — your package manager picks the correct architecture automatically. The quick install script also auto-detects ARM64.

### Quick Install (Any Distro, x86_64 + ARM64)

```bash
curl -fsSL https://raw.githubusercontent.com/patrickjaja/claude-cowork-service/main/scripts/install.sh | bash
```

This auto-detects your architecture (x86_64 or aarch64), downloads the correct binary, installs it to `/usr/local/bin/`, creates a systemd user service, and starts it.

> **Note:** This method does not receive automatic updates. Re-run the command to update manually.

To install without root (uses `~/.local/bin/` instead):
```bash
curl -fsSL https://raw.githubusercontent.com/patrickjaja/claude-cowork-service/main/scripts/install.sh | bash -s -- --user
```

To uninstall:
```bash
curl -fsSL https://raw.githubusercontent.com/patrickjaja/claude-cowork-service/main/scripts/install.sh | bash -s -- --uninstall
```

### From Source

```bash
git clone https://github.com/patrickjaja/claude-cowork-service.git
cd claude-cowork-service
make
sudo make install                  # installs to /usr/bin (default)
# or: sudo make PREFIX=/usr/local install  # installs to /usr/local/bin
```

> **Note:** No automatic updates. Pull and rebuild to update: `git pull && make && sudo make install`.

## Claude Code Dependency

Some Cowork features (e.g. delegating coding tasks) spawn a Claude Code CLI instance. For these features, **Claude Code must be installed** on the host.

On Arch Linux, the AUR package `claude-code` is pulled in automatically as a dependency. If you prefer the npm-installed version (e.g. `npm i -g @anthropic-ai/claude-code`), which ships the minified JS source instead of a pre-built binary, you can uninstall the AUR package after installation — as long as `claude` is on your `$PATH`, Cowork will find it.

Features that **require** Claude Code:
- Delegated coding tasks (Claude Desktop spawns `claude` via `spawn` RPC)
- Any Cowork session that executes CLI commands through the agent

Features that **do not** require Claude Code:
- The daemon itself (socket protocol, session management, event streaming)
- Mount path handling and file operations within sessions

## Quick Start

```bash
# 1. Enable and start the daemon
systemctl --user enable --now claude-cowork

# 2. Install Claude Desktop (if not already)
yay -S claude-desktop-bin

# 3. Open Claude Desktop → Cowork tab → send a message
```

### Verify it's running

```bash
systemctl --user status claude-cowork
```

### Debug mode

```bash
# Stop systemd service and run manually with debug output
systemctl --user stop claude-cowork
cowork-svc-linux -debug
```

## How It Works

The daemon listens on `$XDG_RUNTIME_DIR/cowork-vm-service.sock` and handles 18 RPC methods:

| Method | What it does |
|--------|-------------|
| `configure` | Accepts VM config (ignored — no VM) |
| `createVM` | Creates session directory |
| `startVM` | Emits `vmStarted` + `apiReachability` events |
| `stopVM` | Kills all spawned processes, cleans up |
| `isRunning` | Returns `true` after startVM |
| `isGuestConnected` | Returns `true` after startVM |
| `spawn` | Runs command via `os/exec` on host |
| `kill` | Kills a spawned process (supports signal: SIGTERM, SIGKILL, etc.) |
| `writeStdin` | Writes data to a process's stdin |
| `isProcessRunning` | Checks if a process is alive |
| `mountPath` | Creates symlink (no real mount needed) |
| `readFile` | Reads file from session directory |
| `installSdk` | No-op (SDK already on host) |
| `addApprovedOauthToken` | Stores OAuth token for spawned processes |
| `setDebugLogging` | Toggles verbose logging |
| `isDebugLoggingEnabled` | Returns current debug logging state |
| `subscribeEvents` | Streams process stdout/stderr/exit/startupStep events |
| `getDownloadStatus` | Returns `"ready"` (no bundle needed) |

### What happens during a Cowork session

1. Claude Desktop calls `stopVM` (cleanup), `subscribeEvents`, `startVM`
2. Daemon emits `vmStarted` and `apiReachability` events
3. Claude Desktop calls `spawn` with `/usr/local/bin/claude` and OAuth credentials
4. Daemon remaps the path, resolves the binary, starts `claude` via `os/exec`
5. Claude Desktop sends `writeStdin` with an `initialize` control request (including `sdkMcpServers`), then user messages
6. Claude Code's `stream-json` output (on stderr) is emitted as stdout events back to Claude Desktop
7. SDK MCP tool calls use `control_request`/`control_response` messages embedded in the stdout/stdin streams — Desktop's session manager handles the bidirectional proxy automatically (identical to VM mode)
8. The UI shows the streamed response in real-time

### Path remapping

Claude Desktop assumes a VM with paths like `/sessions/<name>/mnt/...`. The daemon remaps these to `~/.local/share/claude-cowork/sessions/<name>/` with symlinks for mount points.

## Relationship to claude-desktop-bin

This package is an **optional companion** to [claude-desktop-bin](https://github.com/patrickjaja/claude-desktop-bin) (the AUR package for Claude Desktop on Linux).

- **Without this daemon:** Claude Desktop works fine for chat. The Cowork tab shows a helpful error message explaining how to install this package.
- **With this daemon:** Cowork sessions work end-to-end.

The JS patches in claude-desktop-bin that enable Cowork on Linux are:
- `fix_cowork_linux.py` — extends TypeScript VM client to Linux, replaces Windows pipe with Unix socket
- `fix_cowork_error_message.py` — shows Linux-specific guidance when daemon isn't running

## Architecture

```
┌─────────────────────────────┐
│ Claude Desktop (Electron)   │
│  patched with               │
│  claude-desktop-bin          │
└──────────┬──────────────────┘
           │ Length-prefixed JSON
           │ Unix socket
┌──────────▼──────────────────┐
│ cowork-svc-linux (this)     │
│  native.Backend             │
│  └─ os/exec on host         │
└─────────────────────────────┘
```

Compare to Windows/macOS:
```
Claude Desktop → cowork-svc.exe → Hyper-V VM → sdk-daemon (vsock)
Claude Desktop → cowork-svc     → Apple VM   → sdk-daemon (vsock)
Claude Desktop → cowork-svc-linux → direct host execution (no VM)
```

## Protocol Discoveries

During reverse engineering, we found 12 mismatches between the documented/expected protocol and what Claude Desktop actually sends. These are documented here for anyone building compatible implementations:

| # | Discovery | Symptom | Fix |
|---|-----------|---------|-----|
| 1 | Spawn field is `"command"` not `"cmd"` | Empty command, process killed immediately | Fixed JSON tag |
| 2 | Process ID field is `"id"` not `"processId"` in RPC params | writeStdin data never reached process | Fixed JSON tags |
| 3 | Binary path `/usr/local/bin/claude` doesn't exist on host | exec.Command failed | Fallback to `exec.LookPath` |
| 4 | `/sessions/<name>` requires root to create | mkdir failed | Remap to `~/.local/share/claude-cowork/sessions/` |
| 5 | `subscribeEvents` races with `startVM` | Startup events lost, client stuck | Delay event emission by 500ms |
| 6 | Client needs `apiReachability` event (not just `isGuestConnected`) | Client stuck after boot | Emit `apiReachability` during startVM |
| 7 | Args also contain VM paths (not just cwd/env) | `--plugin-dir /sessions/...` unresolvable | Remap args too |
| 8 | Empty env vars (`ANTHROPIC_API_KEY=""`) break auth | Valid OAuth token ignored | Strip empty env vars |
| 9 | `sdkMcpServers` in MCP config blocks Claude Code | Process hangs at init — zero output | ~~Strip SDK servers from config~~ **RESOLVED**: was caused by other bugs (#3, #4, #8); SDK servers now pass through and work via event-stream MCP proxy |
| 10 | Claude Code outputs stream-json on stderr, not stdout | Captured stdout was empty | Emit stderr as stdout events |
| 11 | MCP proxy requests block Claude Code | Process hangs mid-conversation | ~~Auto-respond with error~~ **RESOLVED**: Desktop's session manager handles `control_request`/`control_response` over the event stream natively |
| 12 | Event field is `"id"` not `"processId"` | Events ignored, UI stuck on "Starting up..." | Fixed event JSON tags |

## Dispatch Support

Dispatch lets you send tasks from the Claude mobile app to your Linux desktop. The cowork-service handles spawning and managing all dispatch sessions natively.

### Architecture: The Ditto Orchestrator

Claude Desktop spawns a long-running **dispatch orchestrator agent** (Anthropic internally calls it "Ditto", visible in session directories as `local_ditto_*`). This agent receives messages from the phone, delegates work to child sessions, and sends responses back via the `SendUserMessage` tool.

```
Phone → Anthropic API → SSE → Claude Desktop → wakes Ditto agent
  │
  ├── cowork-service spawns/resumes Ditto CLI session
  │     Ditto has: SendUserMessage, dispatch MCP tools, all SDK MCP servers
  │
  ├── Ditto calls SendUserMessage({message: "..."}) → response appears on phone
  │
  ├── Ditto calls mcp__dispatch__start_task → Desktop asks cowork-service to spawn child
  │     └── Child session does the work (code, files, research, etc.)
  │     └── Child completes → Ditto reads transcript → Ditto replies to phone
  │
  └── Ditto can also use: Gmail, Drive, Chrome, Computer Use, scheduled tasks, etc.
```

### Session types

Desktop uses three session types, identified by the `CLAUDE_CODE_TAGS` environment variable:

| Type | `CLAUDE_CODE_TAGS` | `CLAUDE_CODE_BRIEF` | SendUserMessage | dispatch MCP |
|------|-------------------|---------------------|-----------------|--------------|
| Regular cowork | `lam_session_type:chat` | *(not set)* | No | No |
| Ditto orchestrator | `lam_session_type:agent` | `1` | **Yes** | **Yes** |
| Dispatch child | `lam_session_type:dispatch_child` | *(not set)* | No | No |

### Linux-specific adaptations

These adaptations are applied in `native/backend.go` and `native/process.go`:

**1. Strip `--disallowedTools`**

Desktop passes `--disallowedTools` containing tools that the VM runtime handles:
`AskUserQuestion`, `mcp__cowork__allow_cowork_file_delete`, `mcp__cowork__present_files`,
`mcp__cowork__launch_code_session`, `mcp__cowork__create_artifact`, `mcp__cowork__update_artifact`.

On native Linux there is no VM runtime, so we strip the entire flag — all tools are available to the CLI directly.

**2. Inject `--brief` flag (conditional)**

Desktop passes `CLAUDE_CODE_BRIEF=1` in the environment for Ditto/dispatch agent sessions only (not for regular cowork). The backend detects this and injects the `--brief` CLI flag, which ensures the CLI registers `SendUserMessage` in its tool list. This was broken in CLI v2.1.79–2.1.85, fixed in v2.1.86.

**3. Intercept `present_files` locally**

Desktop's built-in `present_files` MCP handler validates file paths against VM-style mounts and rejects native Linux paths ("not accessible on user's computer"). The backend intercepts `present_files` control_requests in `streamOutput`, verifies the files exist on disk, and returns a synthetic success response directly to the CLI's stdin — bypassing Desktop entirely.

The response includes a hint for the model to use `SendUserMessage` with `attachments` for phone delivery, since `present_files` UI cards only appear in the Desktop app.

**4. Reverse mount path mapping**

The backend builds reverse mount remappings (real host path → VM-style `/sessions/<name>/mnt/<mount>`) applied to outgoing MCP control_requests. This ensures tools other than `present_files` that flow through Desktop's MCP proxy can resolve paths correctly.

### SendUserMessage tool reference

The key tool for dispatch — how the model sends responses to the phone.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `message` | string | Yes | Markdown-formatted message content |
| `attachments` | array | No | File paths (absolute or cwd-relative) for images, diffs, logs |
| `status` | string | No | `"normal"` (replying to user) or `"proactive"` (agent-initiated) |

**Note:** `mcp__dispatch__send_message` is a *different* tool — it sends messages between sessions (inter-agent), not to the user's phone. See [SEND_USER_MESSAGE_STATUS.md](https://github.com/patrickjaja/claude-desktop-bin/blob/master/SEND_USER_MESSAGE_STATUS.md) in claude-desktop-bin for the full investigation.

### Debugging dispatch

```bash
# Run with debug logging to see all spawn args and MCP proxy flow
systemctl --user stop claude-cowork
cowork-svc-linux -debug 2>&1 | tee /tmp/cowork-debug.log

# Check what Desktop passes for each session type
grep 'DISPATCH-DEBUG' /tmp/cowork-debug.log

# Check if present_files interception fires
grep 'present_files handled' /tmp/cowork-debug.log

# Check disallowedTools stripping
grep 'stripping --disallowedTools' /tmp/cowork-debug.log
```

## VM Backend (Dormant)

The `vm/` directory contains a full QEMU/KVM backend implementation:
- `vm/manager.go` — VM lifecycle (create, start, stop)
- `vm/qemu.go` — QEMU instance with direct kernel boot, COW overlays
- `vm/vsock.go` — AF_VSOCK communication with guest sdk-daemon
- `vm/bundle.go` — VHDX→qcow2 conversion, zstd decompression
- `vm/network.go` — QEMU user-mode and bridge networking

This code works but is not used by the native backend. It's retained for potential future sandboxed execution mode.

## Testing

```bash
# Build and run with debug logging
make && ./cowork-svc-linux -debug

# In another terminal, test the protocol
python3 -c "
import socket, struct, json
msg = json.dumps({'method': 'isRunning', 'id': 1}).encode()
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.connect('$(echo \$XDG_RUNTIME_DIR)/cowork-vm-service.sock')
sock.sendall(struct.pack('>I', len(msg)) + msg)
length = struct.unpack('>I', sock.recv(4))[0]
print(json.loads(sock.recv(length)))
"
```

## See Also

- [tweakcc](https://github.com/Piebald-AI/tweakcc) — A great CLI tool for customizing Claude Code (system prompts, themes, UI). Same patching-JS-to-make-it-yours energy. Thanks to the Piebald team for their work.

## Legal Notice

> This is an **unofficial community project** for educational and research purposes.
> Claude Desktop's Cowork feature is proprietary software owned by **Anthropic PBC**.
>
> This repository contains only a reverse-engineered service daemon — not the Claude
> Desktop application itself. A valid Claude account is required to use Cowork.
>
> This project is not affiliated with, endorsed by, or sponsored by Anthropic.
> "Claude" is a trademark of Anthropic PBC.

