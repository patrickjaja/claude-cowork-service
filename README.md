# claude-cowork-service

Native Linux backend for Claude Desktop's **Cowork** feature. Reverse-engineered from Windows `cowork-svc.exe`.

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

### Fedora / RHEL (.rpm)

```bash
# Download from GitHub Releases
wget https://github.com/patrickjaja/claude-cowork-service/releases/latest/download/claude-cowork-service-1.0.0-1.x86_64.rpm
sudo dnf install ./claude-cowork-service-*-1.x86_64.rpm
```

> **Note:** No automatic updates. Download the latest `.rpm` from [GitHub Releases](https://github.com/patrickjaja/claude-cowork-service/releases) to update.

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

Or run directly with Nix:
```bash
nix run github:patrickjaja/claude-cowork-service
```

> **Note:** Update by running `nix flake update` to pull the latest version. `nix run` always fetches the latest.

### Quick Install (Any Distro)

```bash
curl -fsSL https://raw.githubusercontent.com/patrickjaja/claude-cowork-service/main/scripts/install.sh | bash
```

This downloads the pre-built binary, installs it to `/usr/local/bin/`, creates a systemd user service, and starts it.

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

The daemon listens on `$XDG_RUNTIME_DIR/cowork-vm-service.sock` and handles 17 RPC methods:

| Method | What it does |
|--------|-------------|
| `configure` | Accepts VM config (ignored — no VM) |
| `createVM` | Creates session directory |
| `startVM` | Emits `vmStarted` + `apiReachable` events |
| `stopVM` | Kills all spawned processes, cleans up |
| `isRunning` | Returns `true` after startVM |
| `isGuestConnected` | Returns `true` after startVM |
| `spawn` | Runs command via `os/exec` on host |
| `kill` | Kills a spawned process |
| `writeStdin` | Writes data to a process's stdin |
| `isProcessRunning` | Checks if a process is alive |
| `mountPath` | Creates symlink (no real mount needed) |
| `readFile` | Reads file from session directory |
| `installSdk` | No-op (SDK already on host) |
| `addApprovedOauthToken` | Stores OAuth token for spawned processes |
| `setDebugLogging` | Toggles verbose logging |
| `subscribeEvents` | Streams process stdout/stderr/exit events |
| `getDownloadStatus` | Returns "downloaded" (no bundle needed) |

### What happens during a Cowork session

1. Claude Desktop calls `stopVM` (cleanup), `subscribeEvents`, `startVM`
2. Daemon emits `vmStarted` and `apiReachable` events
3. Claude Desktop calls `spawn` with `/usr/local/bin/claude` and OAuth credentials
4. Daemon remaps the path, resolves the binary, starts `claude` via `os/exec`
5. Claude Desktop sends `writeStdin` with an `initialize` control request, then user messages
6. Daemon intercepts and strips `sdkMcpServers` from the initialize request to prevent blocking
7. Claude Code's `stream-json` output (on stderr) is emitted as stdout events back to Claude Desktop
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
| 6 | Client needs `apiReachable` event (not just `isGuestConnected`) | Client stuck after boot | Emit `apiReachable` during startVM |
| 7 | Args also contain VM paths (not just cwd/env) | `--plugin-dir /sessions/...` unresolvable | Remap args too |
| 8 | Empty env vars (`ANTHROPIC_API_KEY=""`) break auth | Valid OAuth token ignored | Strip empty env vars |
| 9 | `sdkMcpServers` in MCP config blocks Claude Code | Process hangs at init — zero output | Strip SDK servers from config |
| 10 | Claude Code outputs stream-json on stderr, not stdout | Captured stdout was empty | Emit stderr as stdout events |
| 11 | MCP proxy requests block Claude Code | Process hangs mid-conversation | Auto-respond with error to unblock |
| 12 | Event field is `"id"` not `"processId"` | Events ignored, UI stuck on "Starting up..." | Fixed event JSON tags |

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

## License

MIT
