# Nexus

Nexus is a remote workspace platform. The daemon runs on a Linux host; a Mac CLI controls it over an SSH tunnel.

## Components

| Component | Package | Description |
|-----------|---------|-------------|
| **Workspace Daemon + CLI** | `packages/nexus` | Go daemon (remote host) + full CLI (local) |
| **Workspace SDK** | `packages/sdk/js` | TypeScript SDK for programmatic workspace control |

## Quick Start

```bash
# Connect CLI to remote daemon
nexus daemon connect newman@linuxbox --port 7777

# Create and start a workspace
nexus workspace create
nexus workspace start <workspace-id>

# Forward workspace ports to local machine
nexus spotlight start <workspace-id>
```

## Build from Source

```bash
cd packages/nexus
go build ./cmd/nexus/...
```

## Docs

- [Docs index](docs/README.md)
- [CLI reference](docs/reference/cli.md)
- [Architecture](ARCHITECTURE.md)
- [Contributing](CONTRIBUTING.md)
