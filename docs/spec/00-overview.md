# Nexus System Specification — Chapter 00: Overview

> **Status**: Normative  
> **Module**: `github.com/oursky/nexus/packages/nexus`

---

## Purpose

Nexus is a remote workspace daemon that manages isolated development environments bound to git
repositories. It exposes a JSON-RPC 2.0 interface over a Unix domain socket and, optionally, over
WebSocket. A CLI client (`nexus`) communicates with the daemon to create, start, stop, fork, and
inspect workspaces, as well as to manage port forwarding (spotlight) and interactive sessions (PTY).

---

## Clause range: `DAEMON-001`–`DAEMON-010`

`**DAEMON-001`** — The Nexus daemon is a single long-running process on a Linux host. It is the
authoritative controller of workspace lifecycle, port forwarding, and PTY sessions.

`**DAEMON-002`** — The CLI (`nexus`) is a client process. It may run on the same host as the daemon
(Unix socket) or on a remote machine (SSH tunnel to WebSocket endpoint).

`**DAEMON-003`** — Three connectivity modes exist:


| Mode                | CLI connects via                                | Authentication                      |
| ------------------- | ----------------------------------------------- | ----------------------------------- |
| Local               | Unix socket (direct)                            | None (filesystem ACL)               |
| Remote (SSH tunnel) | SSH → `ws://localhost:<port>/`                  | Bearer token                        |
| E2E CI              | Direct WebSocket (`NEXUS_E2E_DAEMON_WEBSOCKET`) | Bearer token (`NEXUS_DAEMON_TOKEN`) |


`**DAEMON-004**` — The daemon default data directory is resolved in this order:

1. `$XDG_STATE_HOME/nexus` if `XDG_STATE_HOME` is set
2. `~/.local/state/nexus`
3. `/var/lib/nexus` (fallback when home directory is unavailable)

`**DAEMON-005**` — Default socket path: `<data-dir>/nexusd.sock`.  
Default database path: `<data-dir>/nexus.db`.

`**DAEMON-006**` — The runtime backend is libkrun (VM-based isolation) on Linux unless `--sandbox` is
specified, in which case the process-sandbox backend is used. macOS builds MUST use `--sandbox`
because libkrun is not supported on macOS.

`**DAEMON-007**` — The daemon self-daemonizes by re-executing itself with
`NEXUS_DAEMON_FOREGROUND=1` in the environment. The parent process waits for the socket file to
appear (up to 30 seconds) before returning to the caller. The `--foreground` flag disables this
behavior.

`**DAEMON-008**` — The guest agent binary (`nexus-guest-agent`) is injected into the rootfs
image at daemon startup (libkrun mode on Linux). Injection is skipped when the agent binary hash
matches the cached hash in `<data-dir>/rootfs-agent.sha256`.

`**DAEMON-009**` — The daemon log file is written to `<socket-dir>/daemon.log` in background mode.

`**DAEMON-010**` — The network listener serves HTTP on `<bind>:<port>` (default `127.0.0.1:7777`)
when `--network` is true (the default). The `/healthz` endpoint returns
`{"status": "ok"}` with HTTP 200. The `/version` endpoint returns `{"version": "<version>"}` with
HTTP 200. WebSocket connections upgrade at `GET /`.