# Nexus Daemon (`packages/nexus`)

Go daemon and CLI for remote libkrun workspace orchestration.

## Install (released binaries)

**One-liner** (release assets for Linux and Darwin, amd64/arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/oursky/nexus/main/install.sh | bash
```

This installs `nexus` and `pty-host` into `~/.local/bin` (override with `INSTALL_DIR`). The script picks a suitable SHA-256 tool, uses `sudo` only if the install directory is not user-writable, and on Linux ensures `/data/nexus` exists for the daemon. To pin a version: `curl ... | env NEXUS_VERSION=v0.31.0 bash`, or run from a checkout with `NEXUS_VERSION` set.

Releases older than `pty-host` bundles fall back to `go install` for `pty-host` only (still one command). **`go install` of the main `nexus` binary is not supported** from the module proxy: Linux builds require embedded guest-agent artifacts; use the install script or [GitHub Releases](https://github.com/oursky/nexus/releases) assets.

## What this package provides

- **`nexus` CLI** — full command-line interface for connecting to and managing remote workspaces
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
| `nexus exec`      | Execute a command in a workspace runtime                          |


## Docs

- Full CLI reference: `[docs/reference/cli.md](../../docs/reference/cli.md)`
- Architecture: `[ARCHITECTURE.md](../../ARCHITECTURE.md)`

