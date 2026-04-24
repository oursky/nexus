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

### 2. Connect to your Linux host

Open NexusApp → **Add Host** → enter your SSH connection string (e.g. `user@192.168.1.100`).

The app deploys the daemon automatically on first connection — no manual setup on the Linux host required.

### 3. Create and start a workspace

From the app: **New Workspace** → point it at a Git repository or local path → **Start**.

The workspace boots as an isolated Linux microVM. Your code lives inside it; Docker, your toolchain, and ports are all contained.

### 4. Access your workspace

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

