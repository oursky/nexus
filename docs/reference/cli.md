# CLI Reference

`nexus` is the unified binary for both the workspace client CLI and the remote workspace daemon.

> **Current scope:** The binary today implements the daemon subgroup only. Workspace lifecycle commands (`create`, `shell`, `exec`, etc.) are planned for a future release once the underlying workspace manager packages are rebuilt.

## Commands

### Daemon management

```
nexus daemon start [flags]
```
Starts the Nexus daemon. All flags are optional; a bearer token is auto-generated and
persisted when `--network` is active and no explicit `--token` is given.

```
nexus daemon token
```
Prints the bearer token the daemon uses for network authentication. Generates and persists
a new token if none exists yet. Reads from the OS keyring (D-Bus Secret Service on Linux)
with a file fallback at `~/.config/nexus/daemon-token`.

```
nexus daemon stop
nexus daemon status
```
Stop the running daemon or query its status. _(Not yet implemented.)_

## Environment variables

| Variable | Description |
|---|---|
| `NEXUS_DAEMON_TOKEN` | Bearer token override for `nexus daemon start` (auto-managed when unset) |

## Planned commands (not yet implemented)

The following commands are planned for future releases once the workspace manager and related packages are available. They are listed here for reference only — they do not exist in the current binary.

| Command | Description |
|---|---|
| `nexus create` | Create a workspace from the current directory |
| `nexus list` | List all workspaces |
| `nexus start <id>` | Start a stopped workspace |
| `nexus stop <id>` | Stop a running workspace |
| `nexus remove <id>` | Permanently remove a workspace |
| `nexus restore <id>` | Restore from last snapshot |
| `nexus fork <id> <name>` | Fork a workspace into a new branch |
| `nexus shell <id>` | Open an interactive shell in a workspace |
| `nexus exec <id> -- <cmd>` | Run a single command in a workspace |
| `nexus run -- <cmd>` | Ephemeral workspace; run and discard |
| `nexus tunnel <id>` | Apply compose port forwards |
| `nexus init [path]` | Scaffold `.nexus/` config in a project |
| `nexus doctor` | Health-check the local runtime environment |
| `nexus version` | Print CLI and daemon version info |
| `nexus update` | Self-update the CLI binary |

## `nexus daemon start` flags

### Core flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--db` | string | `~/.local/state/nexus/nexus.db` | SQLite database path |
| `--socket` | string | `~/.local/state/nexus/nexusd.sock` | Unix socket path |
| `--node-name` | string | hostname | Node identity name |
| `--firecracker` | bool | false | Enable Firecracker VM backend |
| `--firecracker-bin` | string | `firecracker` | Firecracker binary name |
| `--kernel` | string | `$NEXUS_FIRECRACKER_KERNEL` | Firecracker kernel image path |
| `--rootfs` | string | `$NEXUS_FIRECRACKER_ROOTFS` | Firecracker rootfs image path |
| `--workdir-root` | string | `~/.local/state/nexus/firecracker-vms` | Firecracker VM work dir root |

### Network listener flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--network` | bool | false | Enable TCP/WebSocket network listener |
| `--bind` | string | `127.0.0.1` | Bind address for the network listener |
| `--port` | int | `7777` | Port for the network listener |
| `--token` | string | _(auto-generated when `--network`)_ | Static bearer token for authentication |
| `--tls` | string | `off` | TLS mode: `off` \| `auto` \| `required` |
| `--tls-cert` | string | — | Path to TLS certificate PEM (`required` mode) |
| `--tls-key` | string | — | Path to TLS key PEM (`required` mode) |

**TLS modes:**

- `off` — plaintext; safe only over loopback or SSH tunnel
- `auto` — self-signed certificate generated in memory at startup
- `required` — use `--tls-cert` / `--tls-key`; falls back to self-signed if files are omitted

**Endpoints exposed by the network listener:**

| Path | Method | Auth | Description |
|---|---|---|---|
| `/healthz` | GET | none | Returns `{"status":"ok"}` |
| `/version` | GET | none | Returns `{"version":"..."}` |
| `/` | WebSocket | Bearer token | JSON-RPC 2.0 entry point |

See [Remote Daemon runbook](../guides/operations.md#remote-daemon-nexus-daemon-start-on-linux) for end-to-end setup.

## Related

- Workspace config: [`workspace-config.md`](workspace-config.md)
- Operations guide: [`../guides/operations.md`](../guides/operations.md)
