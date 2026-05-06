# Bundle Run Setup from Scratch

This document describes how the `nexus bundle run` command works internally and how to set up a host machine from scratch to run self-executing `.nxbundle` files.

## Overview

`nexus bundle run <bundle.nxbundle>` boots a microVM using libkrun, extracts OCI layers into a rootfs, and runs workspace commands (bake + up) inside the VM. The bundle is fully self-contained — no daemon required.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Host                                                       │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐     │
│  │ nexus binary│───▶│  libkrun    │───▶│ microVM     │     │
│  │ (embedded)  │    │  (bundled)  │    │ (libkrunfw) │     │
│  └─────────────┘    └─────────────┘    └──────┬──────┘     │
│       │                                         │           │
│       │    ┌─────────────┐   ┌─────────────┐   │           │
│       └───▶│ gvproxy     │   │ passt       │◀──┘           │
│       (macOS)            │   │ (Linux)     │               │
│                          │   └─────────────┘               │
│       port forwards      │        port forwards            │
│       via HTTP API       │        via --tcp-ports          │
└─────────────────────────────────────────────────────────────┘
```

### Networking by platform

| Platform | Backend | Why |
|----------|---------|-----|
| macOS | **gvproxy** | Provides virtio-net over Unix datagram socket. Full Ethernet support needed for Docker bridge networking. |
| Linux | **passt** | Provides virtio-net over socketpair fd. Replaces TSI because TSI lacks Ethernet frame support (Docker bridge needs `CONFIG_BRIDGE_NETFILTER`). |

### Kernel

| Platform | Architecture | Format | Path searched |
|----------|-------------|--------|---------------|
| macOS | arm64 | `KernelFormatRaw` (Image) | `~/.cache/nexus/kernels/Image-custom` |
| Linux | x86_64 | `KernelFormatElf` (vmlinux) | `~/.cache/nexus/kernels/Image-custom`, `~/.local/share/nexus/vm/vmlinux.bin` |
| Linux | arm64 | `KernelFormatRaw` (Image) | `~/.cache/nexus/kernels/Image-custom` |

If no custom kernel is found, the runner falls back to the kernel bundled inside `libkrunfw`.

**Important:** The bundled libkrunfw kernel does **not** include `CONFIG_BRIDGE` or `CONFIG_IP_NF_NAT`. Docker bridge networking will fail without a custom kernel.

## Prerequisites

### macOS (arm64)

Nothing — fully self-contained.

- `libkrunfw.dylib` + `libkrun.dylib` → extracted from bundle to `~/.cache/nexus/bundles/<hash>/lib/darwin-arm64/`
- `gvproxy` → auto-downloaded to `~/.cache/nexus/bin/gvproxy` on first run
- Custom kernel → must be at `~/.cache/nexus/kernels/Image-custom`

### Linux (x86_64 or arm64)

- `libkrunfw.so` + `libkrun.so` → extracted from bundle to `~/.cache/nexus/bundles/<hash>/lib/linux-amd64/`
- `passt` → auto-downloaded to `~/.cache/nexus/bin/passt` on first run (x86_64 static build only)
- Custom kernel → must be at `~/.cache/nexus/kernels/Image-custom` or `~/.local/share/nexus/vm/vmlinux.bin`

## Building the Custom Kernel

The custom kernel adds Docker networking options (`CONFIG_BRIDGE`, `CONFIG_IP_NF_NAT`, etc.) to libkrunfw's microVM-optimized config.

### macOS / arm64

```bash
# On an arm64 Linux host (or Lima VM on macOS)
git clone https://github.com/smol-machines/libkrunfw.git
KERNEL_VERSION=6.12.76
scripts/nexus/build-kernel.sh /tmp/Image-custom

# Copy to expected path
mkdir -p ~/.cache/nexus/kernels
cp /tmp/Image-custom ~/.cache/nexus/kernels/Image-custom
```

The `build-kernel.sh` script:
1. Downloads Linux source from kernel.org
2. Fetches libkrunfw base config
3. Appends `CONFIG_BRIDGE=y`, `CONFIG_IP_NF_NAT=y`, etc.
4. Runs `make Image` (arm64) or `make vmlinux` (x86_64)

### Linux / x86_64

```bash
# On an x86_64 Linux host with build tools
sudo apt-get install build-essential libncurses-dev bison flex libssl-dev bc libelf-dev

KERNEL_VERSION=6.12.76
scripts/nexus/build-kernel.sh /tmp/vmlinux-custom

mkdir -p ~/.cache/nexus/kernels
cp /tmp/vmlinux-custom ~/.cache/nexus/kernels/Image-custom
```

The runner detects `runtime.GOARCH == "amd64"` and uses `KernelFormatElf`.

### Linux / arm64

Same as macOS/arm64 — `make Image` produces a raw kernel image. Place at `~/.cache/nexus/kernels/Image-custom`.

### Embedded kernel (for daemon mode on Linux)

The `nexus` binary embeds `packages/nexus/cmd/nexus/assets/vmlinux` via `//go:embed`. The daemon extracts this to `~/.local/share/nexus/vm/vmlinux.bin` on first start.

To rebuild and commit the embedded kernel:

```bash
# On an x86_64 Linux host
scripts/nexus/build-kernel.sh packages/nexus/cmd/nexus/assets/vmlinux
git add packages/nexus/cmd/nexus/assets/vmlinux
git commit -m "chore(kernel): rebuild vmlinux with bridge/NAT"
```

## Port Forwarding

### macOS — gvproxy API

The runner starts gvproxy with a control Unix socket (`-listen unix://...ctl`) and sends HTTP requests to expose ports:

```
POST /services/forwarder/expose
{"local":"127.0.0.1:3000","remote":"192.168.127.2:3000","protocol":"tcp"}
```

### Linux — passt CLI

The runner starts passt with `--tcp-ports`:

```bash
passt --fd 3 --tcp-ports 3000,8080
```

The `--fd 3` refers to one end of a `socketpair(AF_UNIX, SOCK_STREAM)` passed via `cmd.ExtraFiles`. The other end is given to libkrun via `krun_set_passt_fd`.

## Troubleshooting

### "can't initialize iptables table `nat'"

The custom kernel is missing `CONFIG_IP_NF_NAT`. Rebuild with `scripts/nexus/build-kernel.sh`.

### "Failed to create bridge docker0 via netlink: operation not supported"

The custom kernel is missing `CONFIG_BRIDGE`. Rebuild.

### "runner: read cached manifest: open .../manifest.json: no such file or directory"

An old `nexus` binary is in your PATH. The bundle shell stub no longer prefers PATH (fixed). Run explicitly:
```bash
/path/to/new/nexus bundle run bundle.nxbundle
```

### DNS not working inside VM

The bundled libkrunfw kernel doesn't support DHCP. The runner writes `/etc/resolv.conf` with the gateway IP (`192.168.127.1` on macOS, auto-configured by passt on Linux). If DNS still fails, check that the networking backend (gvproxy/passt) is running.

### Port not accessible from host

- **macOS**: Check gvproxy logs (`~/.cache/nexus/bundles/<hash>/gvproxy.sock.log`)
- **Linux**: Check that passt is running and `--tcp-ports` includes the right ports
