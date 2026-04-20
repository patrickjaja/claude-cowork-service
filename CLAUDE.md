# CLAUDE.md - Project Guidelines

## Project Overview

This is a native Linux backend daemon for Claude Desktop's Cowork feature. It reverse-engineers the Windows `cowork-svc.exe` (Go/Hyper-V) and implements the same JSON-over-Unix-socket protocol, but executes directly on the host (no VM).

**Language:** Pure Go, zero external dependencies (stdlib only).

**Binary:** `cowork-svc-linux`

**Socket:** `$XDG_RUNTIME_DIR/cowork-vm-service.sock` (falls back to `/tmp/` if `$XDG_RUNTIME_DIR` is unset).

**Session dirs:** `~/.local/share/claude-cowork/sessions/<name>/`

**Protocol:** 22 RPC methods over length-prefixed JSON (4-byte big-endian header, max 10 MB per message).

**Key constraint:** The upstream binary (`cowork-svc.exe`) is managed remotely by Anthropic and changes without notice. Every RPC method, parameter name, and protocol behavior can change between releases. This makes the project inherently fragile --- protocol documentation and handler code must be re-validated on each upstream update.

## Build & Run

```bash
# Build
make build

# Build for ARM64
make build-arm64

# Install (binary + systemd service)
sudo make install

# Run manually in debug mode
cowork-svc-linux -debug

# Run via systemd
systemctl --user start claude-cowork

# Lint
make lint

# Test
make test
```

## Key Files & Purposes

| File / Directory | Purpose |
|---|---|
| `main.go` | Entry point, flag parsing, socket path resolution |
| `native/backend.go` | Core native backend: VM lifecycle simulation, spawn with path remapping, mount handling, `--disallowedTools` stripping, `--brief` injection, `present_files` interception |
| `native/process.go` | Process management: binary resolution (3-stage fallback), stdout/stderr streaming, path remapping, skill prefix stripping, MCP control logging |
| `pipe/server.go` | Unix socket server, connection handling |
| `pipe/handlers.go` | RPC method dispatch, parameter parsing, all struct definitions with JSON tags |
| `pipe/protocol.go` | Wire protocol: `Request`/`Response` types, length-prefixed read/write (4-byte big-endian, max 10 MB) |
| `process/events.go` | Event type definitions (stdout, stderr, exit, apiReachability, error, startupStep) |
| `process/spawn.go` | Legacy process tracker (used by VM mode) |
| `vm/` | Dormant QEMU/KVM backend (manager, qemu, vsock, bundle, network) |
| `scripts/extract-cowork-svc.sh` | Downloads latest Claude Desktop, extracts `bin/` contents |
| `scripts/extract-vm-bundle.sh` | Downloads latest, extracts VM bundle (rootfs, vmlinuz, initrd, config) |
| `bin/` | Extracted upstream binaries (`cowork-svc.exe`, `app.asar`, locale files, icons) with `.version` file |
| `vm-bundle/` | Extracted VM bundle (rootfs.vhdx.zst, vmlinuz.zst, initrd.zst, config) with `.version` file |
| `Makefile` | Build automation (build, build-arm64, install, clean, lint, test) |
| `PKGBUILD` | Arch Linux AUR package definition |
| `flake.nix` + `packaging/nix/` | NixOS flake, module, and evaluation tests |
| `packaging/debian/` | `.deb` build scripts |
| `packaging/rpm/` | `.rpm` build scripts + repo infrastructure |
| `packaging/apt/` | APT repository infrastructure |
| `.upstream-version` | Committed upstream Claude Desktop version (used by CI version-check workflow) |
| `dist/` | Compiled binary + systemd service file |

## Upstream Reference Materials

- `bin/` --- Extracted from Claude Desktop Windows installer (`cowork-svc.exe` lives alongside `app.asar`, locale JSONs, icons)
- `vm-bundle/` --- VM images + config downloaded from Anthropic CDN
- Both directories have `.version` files tracking the Claude Desktop version they were extracted from
- Currently at version **1.3561.0**

## Version-Sensitive Artifacts

These files embed assumptions about upstream internals and **must be re-validated on every upstream release**:

| File | What's fragile | How to verify |
|---|---|---|
| `COWORK_RPC_PROTOCOL.md` | RPC method names, parameters, response shapes | Diff against new `cowork-svc.exe` behavior |
| `COWORK_VM_BUNDLE.md` | VM files, checksums, config format | Compare against new bundle contents |
| `COWORK_SVC_BINARY.md` | Binary behavior, startup sequence, flag handling | Run new binary and observe |
| `native/backend.go` | Spawn parameters, mount handling, path remapping | Test all session types (chat, code, dispatch) |
| `native/process.go` | Event types, output formats, binary resolution | Check new CLI flags and output |
| `pipe/handlers.go` | RPC method set, parameter structs | Add handlers for any new methods |
| `CHANGELOG.md` | Documents all changes per release | Update the `Unreleased` section with upstream changes |

**Rule of thumb:** If a handler references a specific RPC method or parameter name, it may be wrong after the next upstream release. Always verify against the actual protocol traffic.

**CHANGELOG.md:** Every upstream update or code change MUST be documented in `CHANGELOG.md` under the `## Unreleased` section. Use subsections `### Added`, `### Changed`, `### Fixed`, `### Removed` as appropriate. This file is the user-facing record of what changed and why.

## Deep Analysis Workflow

When `bin/` and `vm-bundle/` are updated to a new version, run a deep analysis to update the documentation. Uses `.vm-analysis/` (gitignored) as scratch space.

### bin/ deep dive

```bash
# cowork-svc.exe: Go version, module structure, RPC handlers, dependencies
strings bin/cowork-svc.exe | grep -E "^go[0-9]"
strings bin/cowork-svc.exe | grep "github.com/" | sort -u
strings bin/cowork-svc.exe | grep "handle[A-Z]" | sort -u
sha256sum bin/.version bin/cowork-svc.exe bin/chrome-native-host.exe bin/smol-bin.x64.vhdx bin/default.clod

# app.asar: Electron app version, SDK versions, dependencies
mkdir -p .vm-analysis/asar
npx --yes @electron/asar extract bin/app.asar .vm-analysis/asar
cat .vm-analysis/asar/package.json | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin), indent=2))"
```

### vm-bundle/ deep dive (requires sudo for mount)

```bash
mkdir -p .vm-analysis/rootfs-mount
zstd -d -f vm-bundle/rootfs.vhdx.zst -o .vm-analysis/rootfs.vhdx
qemu-img convert -f vhdx -O raw .vm-analysis/rootfs.vhdx .vm-analysis/rootfs.raw
rm .vm-analysis/rootfs.vhdx  # save space
# Partition 1 offset: check with `fdisk -l .vm-analysis/rootfs.raw`, multiply start sector × 512
sudo mount -o loop,ro,offset=116391936 .vm-analysis/rootfs.raw .vm-analysis/rootfs-mount

# Inside the mounted rootfs, check:
cat .vm-analysis/rootfs-mount/etc/os-release                              # OS version
strings .vm-analysis/rootfs-mount/usr/local/bin/sdk-daemon | grep "^go[0-9]"  # sdk-daemon Go version
.vm-analysis/rootfs-mount/usr/bin/node --version                          # Node.js version
cat .vm-analysis/rootfs-mount/etc/systemd/system/coworkd.service          # systemd unit
sha256sum .vm-analysis/rootfs-mount/usr/local/bin/sdk-daemon              # sdk-daemon checksum
ls .vm-analysis/rootfs-mount/usr/local/lib/node_modules_global/lib/node_modules/  # npm globals
ls .vm-analysis/rootfs-mount/usr/local/lib/python3.10/dist-packages/*.dist-info   # pip packages

# Cleanup
sudo umount .vm-analysis/rootfs-mount
rm -rf .vm-analysis/
```

Update `COWORK_VM_BUNDLE.md`, `COWORK_SVC_BINARY.md`, and `COWORK_RPC_PROTOCOL.md` with any changes found.

## CI-Managed Files (Do NOT Edit Manually)

- `packaging/nix/package.nix` version + hash --- updated by CI release workflow
- `PKGBUILD` pkgver/pkgrel/sha256sums --- updated by CI AUR workflow

## Debugging Workflows

### Debug Mode

Stop the systemd service and run the binary directly to get verbose output:

```bash
systemctl --user stop claude-cowork
cowork-svc-linux -debug
```

### Protocol Test

Send a raw RPC call to verify the socket is responding:

```bash
python3 -c "
import socket, struct, json
msg = json.dumps({'method': 'isRunning', 'id': 1}).encode()
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.connect('$XDG_RUNTIME_DIR/cowork-vm-service.sock')
sock.sendall(struct.pack('>I', len(msg)) + msg)
length = struct.unpack('>I', sock.recv(4))[0]
print(json.loads(sock.recv(length)))
"
```

### Log Files

| Log | Command |
|---|---|
| systemd journal | `journalctl --user -u claude-cowork -f` |
| Desktop cowork log | `tail -f ~/.config/Claude/logs/cowork_vm_node.log` |
| Desktop main log | `tail -f ~/.config/Claude/logs/main.log` |
| Session audit logs | `ls ~/.config/Claude/local-agent-mode-sessions/*/audit.jsonl` |

### Dispatch Debugging

```bash
# Check what Desktop passes for each session type
grep 'DISPATCH-DEBUG' /tmp/cowork-debug.log

# Check disallowedTools stripping
grep 'stripping --disallowedTools' /tmp/cowork-debug.log

# Check brief flag injection
grep 'injecting --brief' /tmp/cowork-debug.log
```

### Common Issues

1. **Socket already in use** --- Another instance is running. Stop it with `systemctl --user stop claude-cowork` or remove the stale socket: `rm $XDG_RUNTIME_DIR/cowork-vm-service.sock`
2. **Claude binary not found** --- 3-stage resolution failed. Ensure `claude` is in PATH, or check `~/.bash_profile` / `~/.bashrc`.
3. **Session stuck on "Starting up..."** --- Check if `subscribeEvents` connected before `startVM`. The 500 ms delay should handle this.
4. **Present files rejected** --- Desktop's `present_files` handler fails on native paths. The interception in `native/backend.go` should handle it --- check debug logs for `"present_files handled"`.

## Architecture Notes

The daemon mimics the Windows `cowork-svc.exe` exactly at the protocol level so Claude Desktop cannot tell the difference. The key design decisions:

- **No VM by default.** The native backend (`native/`) executes `claude` CLI directly on the host, remapping Windows-style paths to Linux paths in all RPC messages.
- **VM backend is dormant.** The `vm/` directory contains a full QEMU/KVM backend that is not wired up in the default build. It exists for future use or experimentation.
- **Zero external dependencies.** The `go.mod` has no `require` directives. Everything uses the Go standard library.
- **Length-prefixed JSON protocol.** Every message on the socket is a 4-byte big-endian length prefix followed by a JSON payload. This matches the Windows named pipe protocol exactly.

## Versioning

- **Project version:** semver (`v1.0.x`), independent of upstream Claude Desktop version
- **Upstream tracking:** `.upstream-version` (committed) tracks the Claude Desktop version for CI; `bin/.version` and `vm-bundle/.version` (gitignored) are local copies created by extract scripts
- **CI version:** from git tags via `-X main.version=$(VERSION)` ldflags
- **Release:** `workflow_dispatch` with patch/minor/major bump

## GitHub Policy

- NEVER update, comment on, or close any GitHub tickets/issues/PRs unless explicitly asked to do so.
