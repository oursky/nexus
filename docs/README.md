# Nexus Docs

Remote workspace platform — run workspaces on a Linux host, controlled from your Mac.

## Quick Start

```bash
# Connect the CLI to a remote daemon
nexus daemon connect newman@linuxbox --port 7777

# Create and start a workspace
nexus workspace create
nexus workspace list
nexus workspace start <workspace-id>

# Forward workspace ports locally
nexus spotlight start <workspace-id>
```

## Reference

| Topic | Doc |
|-------|-----|
| CLI reference | [`docs/reference/cli.md`](reference/cli.md) |
| Architecture | [`ARCHITECTURE.md`](../ARCHITECTURE.md) |
| Contributing | [`CONTRIBUTING.md`](../CONTRIBUTING.md) |
