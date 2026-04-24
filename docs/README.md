# Nexus Docs

Remote workspace platform — run workspaces in isolated Linux microVMs on a Linux host, controlled from your Mac.

## Quick Start

The primary interface is **NexusApp** (macOS). Connect it to a Linux host via SSH; it deploys the daemon and manages workspaces for you.

For CLI / scripting use:

```bash
# Connect the CLI to a remote daemon
nexus daemon connect user@linuxbox --port 7777

# Create and start a workspace
nexus workspace create
nexus workspace list
nexus workspace start <workspace-id>

# Forward workspace ports locally
nexus spotlight start <workspace-id>
```

## Development

Contributor workflows use [go-task](https://taskfile.dev/) at the repo root: `task setup`, `task dev:remote`, `task dev:cli`, `task dev:swift`, plus `task generate:sdk` and `task ci`. Set `REMOTE_HOST` in `.env.local` (see `.env.local.example`). Details are in [Contributing](../CONTRIBUTING.md).

## Reference

| Topic | Doc |
|-------|-----|
| CLI reference | [`docs/reference/cli.md`](reference/cli.md) |
| Architecture | [`ARCHITECTURE.md`](../ARCHITECTURE.md) |
| Contributing | [`CONTRIBUTING.md`](../CONTRIBUTING.md) |
