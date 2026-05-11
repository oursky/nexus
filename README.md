# Nexus

**Run your dev stack on a remote Linux host — managed from your Mac.**  
Workspaces are isolated Linux microVMs (libkrun). Connect, create, and control them from the native macOS app.

---

## Getting Started

### 1. Install NexusApp on your Mac

Download from TestFlight or build from source:

```bash
cd packages/nexus-swift && xcodebuild
```

### 2. Prepare your Linux host

**Storage volume (strongly recommended)**  
Workspace images are multi-GB sparse files. For fast, space-efficient Copy-on-Write (CoW) clones, mount an **XFS** or **btrfs** volume with reflink support at `/data/nexus`:

```bash
# Example: create a 200 GB XFS loop file
sudo mkdir -p /data/nexus
sudo truncate -s 200G /data/nexus.img
sudo mkfs.xfs -f -m reflink=1 /data/nexus.img
sudo mount -o loop /data/nexus.img /data/nexus
sudo chown "$(whoami)" /data/nexus

# Make it permanent in /etc/fstab
# /data/nexus.img /data/nexus xfs loop,defaults 0 0
```

Then start the daemon pointing VM storage at that mount:

```bash
nexus daemon start --workdir-root=/data/nexus/libkrun-vms
```

Without an XFS/btrfs volume, workspace start falls back to sparse `cp` on ext4, which is **much slower** for multi-GB images.

### 3. Connect from the Mac app

Open NexusApp → **Add Host** → enter your SSH connection string (e.g. `user@192.168.1.100`).

The app deploys the daemon automatically on first connection.

### 4. Create and start a workspace

From the app: **New Workspace** → point it at a Git repository or local path → **Start**.

The workspace boots as an isolated Linux microVM. Your code lives inside it; Docker, your toolchain, and ports are all contained.

### 5. Access your workspace

- **Ports** — detected automatically and tunnelled to `localhost` on your Mac.
- **Shell** — open a terminal directly into the VM from the app.
- **Editor** — use the VS Code / Cursor remote extension with the forwarded SSH port.

---

## What it does


| Feature                        | How                                                                               |
| ------------------------------ | --------------------------------------------------------------------------------- |
| **Isolated Linux workspaces**  | Each workspace is a libkrun microVM — full Linux kernel, Docker, separate network |
| **Zero-config daemon deploy**  | NexusApp uploads and starts the daemon on your Linux host over SSH                |
| **Port forwarding**            | Workspace ports are tunnelled to `localhost` on your Mac automatically            |
| **Interactive VM shell**       | Drop into the running VM directly from the app                                    |
| **Git + Docker inside the VM** | Develop, commit, and run containers in full isolation                             |
| **Native macOS app**           | SwiftUI app for workspace management, tunnel status, and spotlight                |


---

## Architecture

```
NexusApp (macOS)
   │  SSH (daemon deploy + control channel)
   │  WebSocket / JSON-RPC 2.0
   ▼
Linux daemon (nexus)   ←  runs on your remote host
   │  vsock
   ▼
libkrun microVM  ←  workspace filesystem (ext4)
   │  Docker bridge
   ▼
Your containers  (web · api · db · …)
```

---

## CLI (power users / CI)

The `nexus` CLI exposes the same operations as the app for scripting:

```bash
# Connect CLI to a running daemon
nexus daemon connect user@your-linux-host

# Workspace lifecycle
nexus workspace create --repo ~/my-project
nexus workspace start <workspace-id>
nexus workspace shell <workspace-id>

# Port forwarding
nexus spotlight start <workspace-id>
```

---

## Contributing

```bash
# Deploy CLI + daemon to remote host, restart daemon
task dev:remote

# Also rebuild CLI locally
task dev:cli

# Also rebuild Swift app
task dev:swift
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full setup, including how to set `REMOTE_HOST` in `.env.local`.

## Docs

- [CLI reference](docs/reference/cli.md)
- [Contributing](CONTRIBUTING.md)


