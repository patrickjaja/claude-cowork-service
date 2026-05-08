# claude-cowork-service

[![Claude Desktop](https://img.shields.io/endpoint?url=https://patrickjaja.github.io/claude-cowork-service/badges/version-check.json)](https://claude.ai/download)
[![AUR version](https://img.shields.io/aur/version/claude-cowork-service)](https://aur.archlinux.org/packages/claude-cowork-service)
[![APT repo](https://img.shields.io/endpoint?url=https://patrickjaja.github.io/claude-cowork-service/badges/apt-repo.json)](https://patrickjaja.github.io/claude-cowork-service/)
[![RPM repo](https://img.shields.io/endpoint?url=https://patrickjaja.github.io/claude-cowork-service/badges/rpm-repo.json)](https://patrickjaja.github.io/claude-cowork-service/)
[![Nix flake](https://img.shields.io/endpoint?url=https://patrickjaja.github.io/claude-cowork-service/badges/nix.json)](https://github.com/patrickjaja/claude-cowork-service/blob/main/flake.nix)
[![Build & Release](https://github.com/patrickjaja/claude-cowork-service/actions/workflows/build-and-release.yml/badge.svg)](https://github.com/patrickjaja/claude-cowork-service/actions/workflows/build-and-release.yml)

Native Linux backend for Claude Desktop's **Cowork** feature. Reverse-engineered from Windows `cowork-svc.exe` bundled with Claude Desktop.

## What This Is

Claude Desktop has a Cowork feature that lets you delegate tasks to a sandboxed Claude Code instance. On macOS it uses Apple's Virtualization framework (Swift), on Windows it uses Hyper-V (Go). On Linux - there's no official support.

This daemon fills that gap. It implements the same length-prefixed JSON-over-Unix-socket protocol that Claude Desktop expects and offers two backends:

- **Native** (default) - runs commands directly on the host, no VM overhead.
- **KVM** (experimental) - runs sessions inside a QEMU/KVM virtual machine, matching the sandboxed execution model of macOS and Windows.

**Key insight:** The VM on macOS/Windows runs Linux anyway. In native mode we skip the VM entirely - because we're already the target OS. In KVM mode we boot the same Anthropic-provided guest image for full parity.

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
> # Claude Code installed via Bun global:
> services.claude-cowork.extraPath = [ pkgs.bun "/path/to/directory/containing/claude" ];
>
> # Claude Code available as a Nix package:
> services.claude-cowork.extraPath = [ pkgs.claude-code ];
> ```
>
> **Dispatch** requires Claude Code >= 2.1.86 (fixes `CLAUDE_CODE_BRIEF` env parsing). If nixpkgs ships an older version, [install Claude Code manually](https://docs.anthropic.com/en/docs/claude-code/overview) and use `extraPath` to point to it.

Or run directly with Nix:
```bash
nix run github:patrickjaja/claude-cowork-service
```

> **Note:** Update by running `nix flake update` to pull the latest version. `nix run` always fetches the latest.

### ARM64 / aarch64 (Raspberry Pi 5, NVIDIA DGX Spark, Jetson, etc.)

```bash
# Debian/Ubuntu ARM64 (via APT repo - automatic updates)
curl -fsSL https://patrickjaja.github.io/claude-cowork-service/install.sh | sudo bash
sudo apt install claude-cowork-service

# Fedora ARM64 (via DNF repo - automatic updates)
curl -fsSL https://patrickjaja.github.io/claude-cowork-service/install-rpm.sh | sudo bash
sudo dnf install claude-cowork-service
```

The APT and DNF repos serve both x86_64 and arm64 packages - your package manager picks the correct architecture automatically. The quick install script and Nix flake also support ARM64 natively.

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

Requires **Go 1.21+** to build.

```bash
git clone https://github.com/patrickjaja/claude-cowork-service.git
cd claude-cowork-service
make
sudo make install                  # installs to /usr/bin (default)
# or: sudo make PREFIX=/usr/local install  # installs to /usr/local/bin
```

> **Note:** No automatic updates. Pull and rebuild to update: `git pull && make && sudo make install`.

### Enable the service

After installing via any method above (except Quick Install, which does this automatically), enable and start the daemon:

```bash
systemctl --user enable --now claude-cowork
```

## Dependencies

| Category | Dependency | Notes |
|----------|-----------|-------|
| **Runtime** | systemd | User service management (`systemctl --user`) |
| **Runtime** | bash | Binary resolution in launcher scripts |
| **Required** | Claude Code CLI | `claude` binary must be in `$PATH` - `bun add --global @anthropic-ai/claude-code` recommended (always latest); declared as `optdepends` in packaging so you control the version |
| **Optional** | socat | Socket health check fallback |
| **Sandbox mode** | `srt` | Packaged installs include the service's sandbox-runtime fork as `/usr/bin/srt`; source installs can build it with `make build-srt` |
| **Sandbox mode** | bubblewrap, socat, ripgrep | Linux dependencies used by sandbox-runtime (`bwrap`, `socat`, `rg`) |
| **KVM mode** | qemu-system-x86_64 | QEMU system emulator (only for `COWORK_VM_BACKEND=kvm`) |
| **KVM mode** | virtiofsd | Virtio filesystem daemon - packaged separately on most distros |
| **KVM mode** | /dev/kvm | KVM kernel module (`kvm`, `kvm_intel` or `kvm_amd`) |
| **KVM mode** | /dev/vhost-vsock | Kernel module: `modprobe vhost_vsock` |
| **Build (from source)** | Go 1.21+ | The daemon is pure Go with no external library dependencies |

## Claude Code Dependency

**Claude Code is required** for Cowork and Dispatch to function. The daemon spawns `claude` CLI instances for every coding task, agent session, and dispatch interaction.

The packaging intentionally declares Claude Code as an **optional dependency** (`optdepends`) rather than a hard dependency. This is because Cowork/Dispatch often requires the **latest** Claude Code release -- if it were a hard `depends`, the package manager would install whatever (potentially outdated) version it has cached, which can cause subtle failures. By keeping it optional, you choose your install method and control the version.

**Recommended install** (always gets the latest version):

```bash
bun add --global @anthropic-ai/claude-code
```

Alternative methods:

- **AUR:** `yay -S claude-code` (version may lag behind the registry release)
- **Nix:** `nix-env -iA nixpkgs.claude-code` (version may lag behind the registry release)

As long as the `claude` binary is on your `$PATH`, the daemon will find it.

Features that **require** Claude Code:
- Delegated coding tasks (Claude Desktop spawns `claude` via `spawn` RPC)
- Any Cowork session that executes CLI commands through the agent
- All Dispatch sessions (orchestrator and child)

Features that **do not** require Claude Code:
- The daemon itself (socket protocol, session management, event streaming)
- Mount path handling and file operations within sessions

## Systemd User Service

The daemon runs as a systemd user service (`claude-cowork.service`). A key detail: **systemd user services do not inherit your desktop session's environment variables by default.** Without the display-related variables, spawned Claude Code processes cannot access the Wayland/X11 display, clipboard, or D-Bus services.

The service file solves this with an `ExecStartPre` that imports the necessary variables from the user session:

```ini
ExecStartPre=-/bin/bash -c 'systemctl --user import-environment WAYLAND_DISPLAY XDG_SESSION_TYPE XDG_CURRENT_DESKTOP DISPLAY DBUS_SESSION_BUS_ADDRESS HYPRLAND_INSTANCE_SIGNATURE SWAYSOCK YDOTOOL_SOCKET 2>/dev/null'
```

| Variable | Purpose |
|----------|---------|
| `WAYLAND_DISPLAY` | Wayland compositor socket |
| `XDG_SESSION_TYPE` | Session type detection (`wayland` / `x11`) |
| `XDG_CURRENT_DESKTOP` | Desktop environment detection (KDE, GNOME, etc.) |
| `DISPLAY` | X11/XWayland display |
| `DBUS_SESSION_BUS_ADDRESS` | D-Bus session bus (clipboard, notifications) |
| `HYPRLAND_INSTANCE_SIGNATURE` | Hyprland-specific IPC |
| `SWAYSOCK` | Sway-specific IPC |
| `YDOTOOL_SOCKET` | ydotool daemon socket (for Computer Use input simulation) |

The leading `-` on `ExecStartPre` means the service still starts even if the import command fails (e.g. some variables may not exist on all setups).

## Verify it's running

```bash
systemctl --user status claude-cowork
```

## Debug mode

```bash
# Stop systemd service and run manually with debug output
systemctl --user stop claude-cowork
cowork-svc-linux -debug
```

## How It Works

The daemon listens on `$XDG_RUNTIME_DIR/cowork-vm-service.sock` (native), `cowork-sandbox-service.sock` (sandbox), or `cowork-kvm-service.sock` (KVM) and handles 22 RPC methods:

| Method | What it does |
|--------|-------------|
| `configure` | Accepts VM config (ignored - no VM) |
| `createVM` | Creates session directory |
| `startVM` | Emits `vmStarted` + `apiReachability` events |
| `stopVM` | Kills all spawned processes, cleans up |
| `isRunning` | Returns `true` after startVM |
| `isGuestConnected` | Returns `true` after startVM |
| `spawn` | Runs command via `os/exec` on host, optionally wrapped by `srt` in sandbox mode |
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
| `getSessionsDiskInfo` | Returns disk usage info for session directories |
| `deleteSessionDirs` | Deletes specified session directories |
| `createDiskImage` | Creates a virtual disk image (KVM mode) |
| `sendGuestResponse` | Handles plugin permission bridge guest responses (no-op on native) |

### What happens during a Cowork session

1. Claude Desktop calls `stopVM` (cleanup), `subscribeEvents`, `startVM`
2. Daemon emits `vmStarted` and `apiReachability` events
3. Claude Desktop calls `spawn` with `/usr/local/bin/claude` and OAuth credentials
4. Daemon remaps the path, resolves the binary, starts `claude` via `os/exec`
5. Claude Desktop sends `writeStdin` with an `initialize` control request (including `sdkMcpServers`), then user messages
6. Claude Code's `stream-json` output (on stderr) is emitted as stdout events back to Claude Desktop
7. SDK MCP tool calls use `control_request`/`control_response` messages embedded in the stdout/stdin streams - Desktop's session manager handles the bidirectional proxy automatically (identical to VM mode)
8. The UI shows the streamed response in real-time

### Path remapping

Claude Desktop assumes a VM with paths like `/sessions/<name>/mnt/...`. The daemon remaps these to `~/.local/share/claude-cowork/sessions/<name>/` with symlinks for mount points.

## Relationship to claude-desktop-bin

This package is an **optional companion** to [claude-desktop-bin](https://github.com/patrickjaja/claude-desktop-bin) (the AUR package for Claude Desktop on Linux).

- **Without this daemon:** Claude Desktop works fine for chat. The Cowork tab shows a helpful error message explaining how to install this package.
- **With this daemon:** Cowork sessions work end-to-end.

The JS patches in claude-desktop-bin that enable Cowork on Linux are:
- `fix_cowork_linux.nim` - extends TypeScript VM client to Linux, replaces Windows pipe with Unix socket
- `fix_cowork_error_message.nim` - shows Linux-specific guidance when daemon isn't running

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
│                             │
│  Native backend (default):  │
│  └─ os/exec on host         │
│                             │
│  Sandbox backend:           │
│  └─ sandbox-runtime (srt)   │
│     └─ os/exec on host      │
│                             │
│  KVM backend (experimental):│
│  └─ QEMU/KVM VM             │
│     └─ sdk-daemon (vsock)   │
└─────────────────────────────┘
```

Compare to Windows/macOS:
```
Claude Desktop → cowork-svc.exe   → Hyper-V VM → sdk-daemon (vsock)
Claude Desktop → cowork-svc       → Apple VM   → sdk-daemon (vsock)
Claude Desktop → cowork-svc-linux → direct host execution (native, default)
Claude Desktop → cowork-svc-linux → sandbox-runtime → host process (sandbox mode)
Claude Desktop → cowork-svc-linux → QEMU/KVM VM → sdk-daemon (vsock, KVM mode)
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
| 9 | `sdkMcpServers` in MCP config blocks Claude Code | Process hangs at init - zero output | ~~Strip SDK servers from config~~ **RESOLVED**: was caused by other bugs (#3, #4, #8); SDK servers now pass through and work via event-stream MCP proxy |
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

On native Linux there is no VM runtime, so we strip the entire flag - all tools are available to the CLI directly.

**2. Inject `--brief` flag (conditional)**

Desktop passes `CLAUDE_CODE_BRIEF=1` in the environment for Ditto/dispatch agent sessions only (not for regular cowork). The backend detects this and injects the `--brief` CLI flag, which ensures the CLI registers `SendUserMessage` in its tool list. This was broken in CLI v2.1.79–2.1.85, fixed in v2.1.86.

**3. Intercept `present_files` locally**

Desktop's built-in `present_files` MCP handler validates file paths against VM-style mounts and rejects native Linux paths ("not accessible on user's computer"). The backend intercepts `present_files` control_requests in `streamOutput`, verifies the files exist on disk, and returns a synthetic success response directly to the CLI's stdin - bypassing Desktop entirely.

The response includes a hint for the model to use `SendUserMessage` with `attachments` for phone delivery, since `present_files` UI cards only appear in the Desktop app.

**4. Reverse mount path mapping**

The backend builds reverse mount remappings (real host path → VM-style `/sessions/<name>/mnt/<mount>`) applied to outgoing MCP control_requests. This ensures tools other than `present_files` that flow through Desktop's MCP proxy can resolve paths correctly.

### SendUserMessage tool reference

The key tool for dispatch - how the model sends responses to the phone.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `message` | string | Yes | Markdown-formatted message content |
| `attachments` | array | No | File paths (absolute or cwd-relative) for images, diffs, logs |
| `status` | string | No | `"normal"` (replying to user) or `"proactive"` (agent-initiated) |

**Note:** `mcp__dispatch__send_message` is a *different* tool - it sends messages between sessions (inter-agent), not to the user's phone. See [SEND_USER_MESSAGE_STATUS.md](https://github.com/patrickjaja/claude-desktop-bin/blob/master/SEND_USER_MESSAGE_STATUS.md) in claude-desktop-bin for the full investigation.

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

## Sandbox Backend (Experimental)

The sandbox backend keeps the native Linux protocol flow, but wraps each spawned Claude Code process with Anthropic's [sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime). It uses Desktop's `allowedDomains` spawn field for network egress and the session's mount modes for filesystem write access.

### Enabling sandbox mode

Packaged installs place the matching sandbox-runtime executable at `/usr/bin/srt`. For source builds:

```bash
make build-srt
sudo install -Dm755 srt/srt-linux-amd64 /usr/local/bin/srt

# Or build both release inputs in a throwaway Docker container
./scripts/build-srt-docker.sh

# Via CLI flag
./cowork-svc-linux -backend=sandbox

# Via environment variable
COWORK_VM_BACKEND=sandbox ./cowork-svc-linux

# If srt is not on PATH
./cowork-svc-linux -backend=sandbox -sandbox-srt=/path/to/srt
```

The backend builds one sandbox-runtime config per spawned process and passes it inline:

```bash
srt --config-json-base64 <config> -c 'cd /sessions/<name>/mnt/<workspace> && exec <claude command>'
```

The sandbox backend writes a customizable baseline policy to `~/.config/claude-cowork-service/sandbox.yaml` on first use, or to `COWORK_SANDBOX_CONFIG` when that environment variable is set. The daemon merges that baseline with each spawn: Desktop's `allowedDomains` extend `network.allowedDomains`, and session mounts extend `linux.bindMounts`. The generated default hides host data roots such as `/home`, gives `/tmp` and `/var/tmp` private writable tmpfs mounts, and re-allows `/var/lib` for system state that many tools expect. Edit the YAML file to adapt those defaults for the local system.

This repo carries a `sandbox-runtime` submodule fork with two Linux extensions the backend uses:

- `--config-json-base64`, so the daemon can pass the full per-spawn config without writing a settings file.
- `linux.bindMounts`, so the sandbox can expose KVM-like paths such as `/sessions/<name>/mnt/<mount>` and `/mnt/.virtiofs-root/shared/<host-path>`.

Sandbox mode listens on `$XDG_RUNTIME_DIR/cowork-sandbox-service.sock`, so it can coexist with native and KVM daemons. Claude Desktop must be patched to probe the sandbox socket when using this mode.

## KVM Backend (Experimental)

Alongside the native and sandbox backends, the daemon includes a real QEMU/KVM backend that runs Cowork sessions inside a virtual machine - matching the sandboxed execution model used on macOS and Windows. The default remains native mode; existing users are unaffected.

### Enabling KVM mode

```bash
# Via CLI flag
./cowork-svc-linux -backend=kvm

# Via environment variable
COWORK_VM_BACKEND=kvm ./cowork-svc-linux

# With debug logging
./cowork-svc-linux -backend=kvm -debug
```

### Configuring the systemd service

To switch the running service to KVM mode (or set other environment variables), use `systemctl edit`:

```bash
systemctl --user edit claude-cowork
```

This opens an editor. Add the following to set the backend:

```ini
[Service]
Environment=COWORK_VM_BACKEND=kvm
```

Save, then restart:

```bash
systemctl --user restart claude-cowork
```

Available environment variables:

| Variable | Values | Default | Description |
|----------|--------|---------|-------------|
| `COWORK_VM_BACKEND` | `native`, `sandbox`, `kvm` | `native` | Backend selection. `native` runs commands directly on the host. `sandbox` wraps host commands with sandbox-runtime. `kvm` runs sessions inside a QEMU/KVM virtual machine. |
| `COWORK_SANDBOX_SRT` | path | `srt` | sandbox-runtime CLI path used by the sandbox backend. |
| `COWORK_SANDBOX_CONFIG` | path | `$XDG_CONFIG_HOME/claude-cowork-service/sandbox.yaml` | Editable sandbox baseline policy merged with each sandbox backend spawn. |
| `COWORK_LOG_FULL` | `1` | *(unset)* | Disable log line truncation (useful for debugging RPC payloads) |

### Prerequisites

| Requirement | Notes |
|-------------|-------|
| `qemu-system-x86_64` | QEMU system emulator |
| `virtiofsd` | Packaged separately from QEMU on most distros (e.g. `pacman -S virtiofsd`, `apt install virtiofsd`) |
| `/dev/kvm` | Must be readable by your user |
| `/dev/vhost-vsock` | Load the kernel module: `modprobe vhost_vsock` |
| Unprivileged user namespaces | The virtiofs helper re-execs itself under `unshare --user --map-root-user --mount` to share `$HOME` with the guest - no host root required |

### Socket path

KVM mode listens on `$XDG_RUNTIME_DIR/cowork-kvm-service.sock`, separate from the native backend's `cowork-vm-service.sock` and the sandbox backend's `cowork-sandbox-service.sock`. This means all backends can coexist on the same machine. Claude Desktop must be patched to probe the KVM socket when using this mode.

### Architecture

The KVM backend is implemented in the `vm/` package:

| File | Role |
|------|------|
| `vm/backend.go` | Session lifecycle, process tracking, host-to-guest RPC |
| `vm/bridge.go` | Length-prefixed JSON over AF_VSOCK with the guest `sdk-daemon` |
| `vm/qemu.go` | QEMU launch, root-disk boot, VHDX-to-qcow2 caching with trailer-canary cache invalidation |
| `vm/qmp.go` | QMP control channel for live networking and shutdown |
| `vm/vfs.go` + `vm/helper.go` | virtiofs `$HOME` share via unprivileged user namespace helper |
| `vm/preflight.go` | Gates startup on `/dev/kvm`, `qemu-system-x86_64`, and vhost-vsock |

### Key design decisions

- **Direct root-disk boot.** The guest boots directly off the converted root disk instead of a COW overlay, so session state persists across reboots.
- **No host root required.** The virtiofs helper uses `unshare --user --map-root-user --mount` to share `$HOME` with the guest without elevated privileges.
- **Centralized logging.** The `logx/` package provides configurable line truncation (default 160 chars) with overflow hints. Use `-log-full-lines` or `COWORK_LOG_FULL=1` to disable truncation when debugging RPC payloads.

### VM bundle

The KVM backend expects a Claude-Desktop-compatible VM bundle under `~/.config/Claude/vm_bundles/`. Claude Desktop downloads this bundle automatically on first launch of the Cowork tab (same provisioning flow as macOS and Windows).

Startup logs are prefixed `[kvm]`; look for `sdk-daemon connected via vsock` to confirm the guest came up.

See [`CHANGELOG.md`](CHANGELOG.md) for the full list of KVM-related changes.

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

## Troubleshooting

### Wayland / Computer Use issues

**Claude Code can't access the display in cowork sessions:**

The systemd environment may be missing display variables. Check what's imported:

```bash
systemctl --user show-environment | grep WAYLAND
systemctl --user show-environment | grep DISPLAY
```

If the variables are missing, import them manually and restart the service:

```bash
systemctl --user import-environment WAYLAND_DISPLAY XDG_SESSION_TYPE DISPLAY DBUS_SESSION_BUS_ADDRESS
systemctl --user restart claude-cowork
```

**ydotool doesn't work in cowork sessions (Computer Use):**

Check that the `YDOTOOL_SOCKET` variable is in the systemd environment:

```bash
systemctl --user show-environment | grep YDOTOOL
```

If missing, ensure the ydotool daemon is running and import the socket path:

```bash
systemctl --user import-environment YDOTOOL_SOCKET
systemctl --user restart claude-cowork
```

**`claude` binary isn't found:**

The daemon resolves `claude` from `$PATH` via `bash -lc`. Verify it's accessible:

```bash
bash -lc "which claude"
```

If this prints nothing, Claude Code is either not installed or not in your login shell's `$PATH`. See the [Claude Code Dependency](#claude-code-dependency) section for install options.

## Upstream Reference Docs

Deep analysis of the upstream Windows binaries and VM bundle we reverse-engineer against:

| Document | What it tracks |
|----------|---------------|
| [COWORK_RPC_PROTOCOL.md](COWORK_RPC_PROTOCOL.md) | All 22 RPC methods, event types, protocol discoveries, Linux adaptations |
| [COWORK_SVC_BINARY.md](COWORK_SVC_BINARY.md) | `cowork-svc.exe` Go internals, handler functions, app.asar SDK versions, checksums |
| [COWORK_VM_BUNDLE.md](COWORK_VM_BUNDLE.md) | VM rootfs contents - sdk-daemon, Node.js, Python packages, system packages, checksums |

These are re-validated on every upstream Claude Desktop release. See [update-prompt.md](update-prompt.md) for the update workflow.

## See Also

- [tweakcc](https://github.com/Piebald-AI/tweakcc) - A great CLI tool for customizing Claude Code (system prompts, themes, UI). Same patching-JS-to-make-it-yours energy. Thanks to the Piebald team for their work.

## Legal Notice

> This is an **unofficial community project** for educational and research purposes.
> Claude Desktop's Cowork feature is proprietary software owned by **Anthropic PBC**.
>
> This repository contains only a reverse-engineered service daemon - not the Claude
> Desktop application itself. A valid Claude account is required to use Cowork.
>
> This project is not affiliated with, endorsed by, or sponsored by Anthropic.
> "Claude" is a trademark of Anthropic PBC.
