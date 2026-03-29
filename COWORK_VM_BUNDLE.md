# Cowork VM Bundle Reference — v1.1.9493

> Re-validate on every upstream Claude Desktop version update.

## Bundle Architecture

- Claude Desktop embeds a VM bundle config in its app.asar JS
- Config contains: SHA commit hash, file lists per platform/arch with checksums
- VM images are downloaded from Anthropic CDN on first use
- URL pattern: `https://downloads.claude.ai/vms/linux/<arch>/<sha>/<filename>.zst`

## Current Config (v1.1.9493)

- **SHA:** `fb30784dadb34104626c8cf6d8f90dd47cd393cc`
- **Platforms:** darwin (arm64, x64), win32 (arm64, x64)

**darwin/arm64:**

| File | Checksum (SHA256) |
|------|------------------|
| rootfs.img | e77049d2784801ad3df8baf0e875e34430731c798e1ca5809af3e549086e2b97 |

**darwin/x64:**

| File | Checksum |
|------|----------|
| rootfs.img | 231c5ac45e5de77c621edeb0cbb05361068203a5ee71488e88ee19e10970ed2d |

**win32/arm64:**

| File | Checksum | Progress |
|------|----------|----------|
| rootfs.vhdx | 1551d438829b6ae234a9145ab1768394edb1c20ee5255ce342ac3f99c4abf351 | 0-80% |
| vmlinuz | 2af5854ca028473b0e5405b56a6eb034000bce6a44902f80e4f76c47e00031f7 | 80-90% |
| initrd | 288d40bdcc39ec34831f81f42f6d872b0d5d58f1323ddfa70a75927edd463602 | 90-100% |

**win32/x64:**

| File | Checksum | Progress |
|------|----------|----------|
| rootfs.vhdx | 1d49e23b4cce8865412c201afa396df6112adb008b1476ec639df19b23c46be4 | 0-80% |
| vmlinuz | bddf9d0ac039023ecba76b0056648d84dd434a98dcf24dc0e2e99c8c475987b7 | 80-90% |
| initrd | 728bdde36326fe7ef949c3719681413ac91f1756cc3c042b7c7bee9a17ceb028 | 90-100% |

## File Descriptions

- **rootfs.vhdx / rootfs.img** — Root filesystem (Hyper-V VHDX on Windows, raw image on macOS). Contains Linux userland with sdk-daemon, node, Claude Code CLI
- **vmlinuz** — Linux kernel (Windows/Hyper-V only, macOS uses Apple Virtualization's built-in kernel)
- **initrd** — Initial ramdisk (Windows/Hyper-V only)

## Extraction

- Extract script: `./scripts/extract-vm-bundle.sh [--arch x64|arm64]`
- Files are zstd-compressed on CDN (`.zst` suffix)
- Extracted to: `vm-bundle/` directory
- Config saved as: `vm-bundle/vm-bundle-config.json`
- app.asar also extracted for investigation: `vm-bundle/app-asar-extracted/`

## Local Files (after extraction)

```
vm-bundle/
├── rootfs.vhdx.zst          (2.2 GB compressed)
├── vmlinuz.zst              (15 MB compressed)
├── initrd.zst               (164 MB compressed)
├── vm-bundle-config.json    (parsed from app.asar JS)
├── app-asar-extracted/      (full Electron app for investigation)
└── .version                 (Claude Desktop version, e.g. "1.1.9493")
```

## How to Parse Config from JS

The config is embedded as a minified object in index.js:

```javascript
const qn = {sha:"fb30784...",files:{darwin:{arm64:[{name:"rootfs.img",checksum:"...",progressStart:0,progressEnd:100}],...},...}}
```

The extract script finds it with regex: `{sha:"[a-f0-9]{40}",files:{`

## What Changes Between Versions

- **SHA** — Changes when Anthropic updates the VM images (can happen independently of Claude Desktop version)
- **Checksums** — Change when individual files are rebuilt
- **File list** — Rarely changes, but new platforms/architectures may appear
- **Config format** — The progressStart/progressEnd fields are for UI progress bars

---

## VM Rootfs Deep Dive (v1.1.9493)

### Base System

| Property | Value |
|----------|-------|
| **Base OS** | Ubuntu 22.04.5 LTS (Jammy Jellyfish) |
| **Hostname** | `claude` |
| **Kernel** | 6.8.0-94-generic (Ubuntu SMP, built by buildd@lcy02-amd64-114, Jan 16 2025) |
| **Total disk size** | 10 GB (6.9 GB used) |
| **Filesystem** | ext4, label `cloudimg-rootfs` |

### Partition Layout (GPT)

| Partition | Size | Type |
|-----------|------|------|
| Partition 14 | 4 MB | BIOS boot |
| Partition 15 | 106 MB | EFI System (UEFI, vfat) |
| Partition 1 | 9.9 GB | Linux filesystem (ext4, main rootfs) |

### Key Binaries in VM

| Binary | Path | Version | Size | SHA256 |
|--------|------|---------|------|--------|
| sdk-daemon | /usr/local/bin/sdk-daemon | Go 1.23.12 | 7.1 MB | ff7c6e1d5016eecf4c32d3f5aaf0c0df8c9e3f16b4512d7b6381c9ac99cf77bd |
| sandbox-helper | /usr/local/bin/sandbox-helper | (restricted) | 2.0 MB | (permission denied) |
| Node.js | /usr/bin/node | v22.22.0 | — | 1bec56ef7cfa9a76f3e0b7c0a87f220eb73f23102b9c0b4c7529a3f7c3ce7c31 |
| Python | /usr/bin/python3.10 | 3.10 | 5.9 MB | — |
| uv (Astral) | /usr/local/bin/uv | 0.5.x | 55 MB | ae65ed04fee535f3ab8d31da7c2f9fde156dc5afdd6b5b5125e535ccc49bba34 |
| magika (Google) | /usr/local/bin/magika | 0.6.3 | 32 MB | 0bbacaccd0cbf666bb9371fac958395a1c66b590ed05e4393ce3415e9b68f0d6 |

### sdk-daemon Details

- **Go version:** go1.23.12
- **Module path:** (stripped binary, no module path exposed)
- **Dependencies:** github.com/elazarl/goproxy (MITM proxy), github.com/mdlayher/vsock v1.2.1
- **systemd service:** `coworkd.service` — runs as root, ExecStart=/usr/local/bin/sdk-daemon, Restart=always, RestartSec=3
- **Communication:** vsock (AF_VSOCK), uses github.com/mdlayher/vsock Go library
- **Features:** Process spawning, MITM proxy for HTTPS inspection, CA certificate injection, 9P/Plan9 file sharing, smol-bin updater, cgroup management, SSH key injection
- **Strings found:** "PROBABLY_UNREACHABLE", "REACHABLE", "CONNECTED", event/notification/spawn/kill methods, virtiofs mount support

### coworkd.service (systemd unit)

```ini
[Unit]
Description=coworkd - vsock RPC bridge for process management
After=network.target local-fs.target systemd-udev-settle.service
Wants=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/sdk-daemon
Restart=always
RestartSec=3
User=root
Group=root
Environment=HOME=/root

[Install]
WantedBy=multi-user.target
```

### Global Node.js Packages

| Package | Purpose |
|---------|---------|
| @anthropic-ai/* | Anthropic SDK (agent SDK, etc.) |
| docx | Word document generation |
| graphviz | Graph visualization |
| markdown-toc | Markdown table of contents |
| marked | Markdown parser |
| pdfjs-dist | PDF rendering |
| pdf-lib | PDF creation/modification |
| pptxgenjs | PowerPoint generation |
| sharp | Image processing |
| ts-node | TypeScript execution |
| tsx | TypeScript execution |
| typescript | TypeScript compiler |

### Python Packages (pip, 60+ packages)

Key packages with versions:

- **Document processing:** camelot-py 1.0.9, markitdown 0.1.4, pdfplumber 0.11.9, pdfminer-six 20251230, pypdf 3.17.4, pypdfium2 5.4.0, openpyxl 3.1.5, odfpy 1.4.1, pdf2image 1.17.0, pikepdf 10.3.0
- **Image/ML:** opencv-python 4.13.0.92, pillow 12.1.1, magika 0.6.3, onnxruntime 1.23.2, imageio 2.37.2
- **Data:** pandas 2.3.3, numpy 2.2.6, matplotlib 3.10.8
- **Text:** beautifulsoup4 4.14.3, lxml 6.0.2, markdown 3.10.2, markdownify 1.2.2, mistune 3.2.0
- **Crypto:** cryptography 46.0.5, certifi 2026.1.4
- **Math:** sympy (via isympy), mpmath 1.3.0
- **Office:** pyoo 1.4 (LibreOffice Python bridge)

### System Packages (APT)

Key installed packages: build-essential, curl, ffmpeg, gcc-11, ghostscript, git, imagemagick, libreoffice (calc, core, base-core), poppler, python3.10-dev, tesseract (via pytesseract), openssh

### Network Configuration

- Interface: enp0s1 (DHCP via systemd-networkd)
- Fstab: LABEL=cloudimg-rootfs -> / (ext4), LABEL=UEFI -> /boot/efi (vfat)

### User Configuration

- Default user: `ubuntu` (UID matches host user)
- Home: /home/ubuntu with .ssh/authorized_keys (empty), .bashrc, .npmrc
- .npmrc: cache=/home/ubuntu/.npm, prefix=/usr/local/lib/node_modules_global

---

## What to Check on Update

1. Compare SHA with previous version
2. If SHA changed: checksums likely changed too — note which files
3. Check for new platforms or architectures
4. Check if darwin now also has vmlinuz/initrd (would indicate architecture change)
5. Check sdk-daemon version: `strings rootfs-mount/usr/local/bin/sdk-daemon | grep "^go[0-9]"`
6. Check Node.js version: `rootfs-mount/usr/bin/node --version`
7. Check Python packages: `ls rootfs-mount/usr/local/lib/python3.10/dist-packages/*.dist-info`
8. Check global npm packages: `ls rootfs-mount/usr/local/lib/node_modules_global/lib/node_modules/`
9. Compare sdk-daemon SHA256 checksum
10. Check coworkd.service for changes
11. Check for new binaries in /usr/local/bin/
12. Update this document

## Version History

| Claude Desktop Version | VM Bundle SHA | Notable Changes |
|----------------------|--------------|-----------------|
| 1.1.9493 | fb30784dadb34104626c8cf6d8f90dd47cd393cc | Current |
| 1.1.9310 | (check previous commit) | Previous |
