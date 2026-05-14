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

**Linux daemon hosts:** the VM driver expects persistent storage under `/data/nexus` (default workspace VM path includes `/data/nexus/default`). The installer **always** creates `/data/nexus` and `/data/nexus/default` on Linux, using `sudo` when needed, and assigns ownership to your user. Production machines should mount **XFS with reflink=1** (or reflink-capable btrfs) on `/data` so copy-on-write microVM images perform correctly—the install script cannot create that filesystem layout for you, but Nexus requires that store to exist before workspaces run.

The script uses `sudo` only when the install destination or `/data` paths are not user-writable.

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

## What it does

| Feature | How |
| ------- | --- |
| **Isolated Linux workspaces** | Lightweight libkrun microVMs — Linux kernel, Docker, isolated network |
| **CLI / TUI** | Full lifecycle: daemon, workspaces, port forwards (`spotlight`), exec |
| **Git + Docker inside the VM** | Develop and run containers in each microVM |

---

## Architecture (conceptual)

```mermaid
flowchart TD
  subgraph user["Your machine"]
    CLI["nexus CLI / TUI"]
  end
  subgraph engine["Linux engine host"]
    D["nexus daemon"]
    VM["libkrun microVMs"]
    WS["Workspace runtimes"]
  end
  CLI -->|"SSH, profile, bearer token"| D
  D --> VM
  VM --> WS
  WS --> Stack["Repos, Docker, tooling"]
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
