# Nexus

**Remote Linux workspaces — CLI and TUI.**  
Orchestrate isolated dev environments (including libkrun microVMs) from the terminal.

---

## Install (one-liner)

```bash
curl -fsSL https://raw.githubusercontent.com/oursky/nexus/main/install.sh | bash
```

Installs `nexus` and `pty-host` into `~/.local/bin` by default. Override the destination with `INSTALL_DIR`, pin a release with `NEXUS_VERSION`, or fork the install via `GITHUB_REPOSITORY`.

```bash
curl -fsSL https://raw.githubusercontent.com/oursky/nexus/main/install.sh | env INSTALL_DIR=/usr/local/bin bash
```

On Linux, the installer creates `/data/nexus` owned by your user when missing (daemon VM backing store). The script uses `sudo` only when the install directory is not user-writable.

---

## Getting started (CLI)

```bash
# Point the CLI at a daemon (SSH target depends on your deployment)
nexus daemon connect user@your-linux-host

nexus workspace create --repo ~/my-project
nexus workspace start <workspace-id>
nexus workspace shell <workspace-id>
```

See [CLI reference](docs/reference/cli.md) for the full command tree.

---

## Linux host: fast VM storage (optional)

For fastest copy-on-write workspace images with libkrun, mount **XFS** (with reflink enabled) or **btrfs** at `/data` so `/data/nexus` sits on that filesystem. The install script always ensures `/data/nexus` exists; reflink-capable storage underneath is a host-level tuning step.

---

## What it does

| Feature | How |
| ------- | --- |
| **Isolated Linux workspaces** | libkrun microVMs — Linux kernel, Docker, isolated network (when using the VM driver) |
| **CLI / TUI** | Full lifecycle: daemon, workspaces, port forwards (`spotlight`), exec |
| **Git + Docker inside the VM** | Develop and run containers in isolation |

---

## Architecture (conceptual)

```
nexus CLI (your machine)
   │  SSH / JSON-RPC (profile + token)
   ▼
Linux daemon (`nexus`)
   │  vsock / driver
   ▼
Workspace runtime (e.g. libkrun microVM)
   ▼
Your stack (containers, tools, …)
```

---

## Contributing

```bash
task dev:local    # local daemon + CLI (typical Linux dev)
task dev:cli      # CLI only
task build && task test
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full setup.

## Docs

- [CLI reference](docs/reference/cli.md)
- [Contributing](CONTRIBUTING.md)
