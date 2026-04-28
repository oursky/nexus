# Nexus System Specification — Chapter 01: Domain Concepts

> **Status**: Normative  
> RFC-2119 keywords apply throughout: MUST, MUST NOT, SHOULD, MAY.

---

## Workspace — `WS-001`–`WS-009`

`**WS-001`** — A **workspace** is the primary runtime unit. It binds a git repository at a specific
ref to a runtime backend instance (libkrun micro-VM or process sandbox).

`**WS-002`** — Every workspace has a globally unique string ID (UUID format). IDs MUST NOT be
reused, even after a workspace enters state `removed`.

`**WS-003**` — A workspace's `repo` (URL or local path) and `ref` (git branch, tag, or SHA) are set
at creation time. They identify the source of the workspace's content.

`**WS-004**` — `workspaceName` is a human-readable label. It MUST be unique among workspaces not
in state `removed` at any given time.

`**WS-005**` — A workspace MUST be in exactly one `state` at all times: `created`, `starting`,
`running`, `paused`, `stopped`, `restored`, or `removed`. See `02-state-machines.md` for the full
state machine.

`**WS-006**` — `projectId` optionally associates a workspace with a project. If set, the project
MUST have existed at workspace creation time (stale references are not actively enforced after the
project is removed).

`**WS-007**` — `parentWorkspaceId` and `lineageRootId` are set for forked workspaces:

- `parentWorkspaceId`: the ID of the workspace from which this was forked
- `lineageRootId`: the ID of the original (non-forked) ancestor; for a root workspace,
`lineageRootId == id`

`**WS-008**` — `backend` specifies the runtime: `"libkrun"` or `"process"`. If omitted at
creation time, the daemon selects based on its startup configuration.

`**WS-009**` — `policy` controls workspace automation behaviour:

- `autoStop` (bool): automatically stop after `autoStopDelay`
- `autoStopDelay` (duration string)
- `isolationLevel` (string)
- `maxLifetimeSec` (int)

---

## Project — `PRJ-001`–`PRJ-006`

`**PRJ-001**` — A **project** is an optional organizing concept representing a tracked repository
registration. Workspaces may exist without a project.

`**PRJ-002**` — Every project has a globally unique string ID (UUID format).

`**PRJ-003**` — `name` is a human-readable label. It MUST be unique at all times (not scoped to
non-removed state as with workspaces — projects are hard-deleted).

`**PRJ-004**` — `repoUrl` is the canonical URL for the repository. It MUST be non-empty.

`**PRJ-005**` — `rootPath` is an optional local filesystem path on the daemon host.

`**PRJ-006**` — `config.defaultBackend` and `config.defaultRef` are advisory defaults for workspace
creation and are not enforced by the daemon.

---

## Spotlight (Port Forwarding) — `SPOT-001`–`SPOT-009`

`**SPOT-001**` — **Spotlight** is the system for exposing ports from a running workspace runtime to
the daemon host's network, and optionally further to the developer's local machine via SSH tunnel.

`**SPOT-002**` — A **forward** is a single active port-forwarding rule. Every forward has a unique
ID of the form `spot-<nanoseconds>`.

`**SPOT-003**` — A forward MUST be associated with an existing workspace.

`**SPOT-004**` — `localPort` is the port on the daemon host. `remotePort` is the port inside the
workspace runtime. `protocol` is `"tcp"` (default), `"udp"`, or `"http"`.

`**SPOT-005**` — In process-sandbox backend mode, the daemon binds `127.0.0.1:<localPort>` and
proxies TCP connections to `127.0.0.1:<remotePort>` inside the workspace process namespace.

`**SPOT-006**` — In libkrun VM mode, the spotlight service uses the VM's vsock connection
(port `10792`) to reach the guest. The returned `Forward.targetHost` holds the resolved guest
endpoint. SSH tunnels from the CLI target `targetHost:remotePort` on the daemon host.

`**SPOT-007**` — `Forward.state` is one of: `"active"`, `"inactive"`.

`**SPOT-008**` — The `spotlight.stop` RPC closes **all** active forwards for a workspace atomically.
There is no per-forward close at the RPC level; per-forward close uses `workspace.ports.remove`.

`**SPOT-009**` — The client (`nexus spotlight start`) persists the active workspace ID to
`<data-dir>/spotlight-client-state.json` keyed by `"<host>|<port>|<sshPort>"`. This is used to
tear down the previous spotlight session when a new one is started.

---

## PTY Sessions — `PTY-001`–`PTY-009`

`**PTY-001**` — A **PTY session** is an interactive (or non-interactive) terminal session attached
to a workspace runtime.

`**PTY-002**` — Every PTY session has a unique string ID of the form `pty-<nanoseconds>`.

`**PTY-003**` — PTY sessions are in-memory only; they do NOT survive daemon restart.

`**PTY-004**` — A PTY session MAY be created for any workspace regardless of state; however, the
underlying shell process will fail to start if the workspace runtime is not running.

`**PTY-005**` — Default terminal size: 80 columns × 24 rows.

`**PTY-006**` — Data flows bidirectionally: the client writes via `pty.write` (raw string, not
base64); the daemon pushes output via `pty.data` server-push notifications (`data` field is also
raw string, not base64).

`**PTY-007**` — When a PTY session exits, the daemon MUST push a `pty.exit` notification with
`{sessionId, exitCode: int}`.

`**PTY-008**` — In libkrun VM mode, PTY sessions communicate with the guest via a vsock
connection to the agent. The guest agent protocol uses JSON envelopes with `shell.open`,
`shell.write`, `shell.resize`, `shell.close` message types. This protocol is internal and not
part of the public RPC surface.

`**PTY-009**` — When `args` is empty, the shell is launched interactively with `-l`. When `args`
is provided, the shell executes the specified command and exits when it completes.

---

## Auth Relay — `AUTH-001`–`AUTH-005`

`**AUTH-001**` — The **auth relay** issues short-lived bearer tokens tied to a workspace's auth
bindings, enabling secure temporary access delegation.

`**AUTH-002**` — Tokens are opaque strings. Their internal format is implementation-defined.

`**AUTH-003**` — Tokens are valid only on the daemon instance that issued them.

`**AUTH-004**` — Expired tokens MUST be treated identically to revoked tokens from the client's
perspective.

`**AUTH-005**` — The auth relay token is distinct from the daemon bearer token used to authenticate
WebSocket connections.
