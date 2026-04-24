# Nexus Daemon (`packages/nexus`)

Go daemon and CLI for remote libkrun workspace orchestration.

## What this package provides

- `**nexus` CLI** — full command-line interface for connecting to and managing remote workspaces
- **Daemon** — Go server that runs on the remote Linux host, managing workspace lifecycle, port forwards, and PTY sessions
- **4-layer internal architecture** — domain → infra → app → rpc

## Build

```bash
cd packages/nexus
go build ./cmd/nexus/...
```

From the **repository root**, `task build` runs `go build ./...` in this package. Use `task test` and `task ci` for checks; see the root [CONTRIBUTING.md](../../CONTRIBUTING.md) for remote deploy tasks (`dev:remote`, `dev:cli`).

## Test

```bash
cd packages/nexus
go test ./...
```

## CLI command groups


| Group             | Description                                                       |
| ----------------- | ----------------------------------------------------------------- |
| `nexus daemon`    | Connect/disconnect remote daemon, manage daemon process           |
| `nexus workspace` | Full workspace lifecycle (create, start, stop, fork, shell, etc.) |
| `nexus spotlight` | Port-forward management (start, stop, list, per-port controls)    |
| `nexus project`   | Project CRUD                                                      |
| `nexus init`      | Initialize `.nexus/` workspace metadata                           |
| `nexus exec`      | Execute a command in a workspace runtime                          |
| `nexus doctor`    | Run readiness checks against a workspace                          |


## Docs

- Full CLI reference: `[docs/reference/cli.md](../../docs/reference/cli.md)`
- Architecture: `[ARCHITECTURE.md](../../ARCHITECTURE.md)`

