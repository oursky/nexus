# Nexus System Specification — Chapter 06: Error Taxonomy

> **Status**: Normative

---

## General rules — `ERR-001`–`ERR-005`

**`ERR-001`** — Every RPC error response MUST use JSON-RPC error code `-32000` for application
errors. Protocol errors use standard codes (`-32700`, `-32600`, `-32601`, `-32602`).

**`ERR-002`** — When `data` is present on an error response, it MUST contain a `"kind"` string
that is machine-readable and stable across releases.

**`ERR-003`** — The `"message"` field is human-readable and MAY change between releases. Clients
MUST NOT parse `message` for programmatic decisions; use `data.kind` instead.

**`ERR-004`** — Protocol error codes:
- `-32700` — Parse error (malformed JSON)
- `-32600` — Invalid Request
- `-32601` — Method Not Found
- `-32602` — Invalid Params (missing required field, wrong type)

**`ERR-005`** — All application errors use code `-32000` with `data.kind` identifying the specific
condition.

---

## Workspace errors — `ERR-010`–`ERR-025`

| Clause | `data.kind` | Condition | Returned by |
|--------|------------|-----------|-------------|
| `ERR-010` | `workspace.duplicate_name` | `workspaceName` already in use by non-removed workspace | `workspace.create` |
| `ERR-011` | `workspace.not_found` | No workspace with given ID exists | `workspace.info`, `workspace.start`, `workspace.stop`, `workspace.remove`, `workspace.fork`, `workspace.restore`, `workspace.ready` |
| `ERR-012` | `workspace.invalid_state` | Operation not valid in current state | `workspace.start` (already running), `workspace.stop` (not running), `workspace.remove` (running), `workspace.fork` (not running), `workspace.restore` (not stopped/created) |
| `ERR-013` | `workspace.invalid_spec` | Create spec validation failed (e.g. empty repo or name) | `workspace.create` |
| `ERR-014` | `workspace.fork_missing_ref` | `childRef` omitted on fork | `workspace.fork` |
| `ERR-015` | `workspace.no_snapshot` | No snapshot exists for restore | `workspace.restore` |

> Note: `ERR-011` and `ERR-012` use the same underlying sentinel `ErrNotFound` /
> `ErrInvalidTransition` mapped at the RPC layer. Clients should use `data.kind` to distinguish.

---

## Project errors — `ERR-040`–`ERR-045`

| Clause | `data.kind` | Condition | Returned by |
|--------|------------|-----------|-------------|
| `ERR-040` | `project.duplicate_name` | Project name already in use | `project.create` |
| `ERR-041` | `project.invalid_spec` | `repoUrl` is empty | `project.create` |
| `ERR-042` | `project.not_found` | No project with given ID | `project.get`, `project.remove` |

---

## Spotlight errors — `ERR-050`–`ERR-055`

| Clause | `data.kind` | Condition | Returned by |
|--------|------------|-----------|-------------|
| `ERR-050` | `spotlight.workspace_not_found` | Workspace not found when adding forward | `spotlight.start`, `workspace.ports.add` |
| `ERR-051` | `spotlight.port_conflict` | `localPort` already bound by another active forward | `spotlight.start`, `workspace.ports.add` |
| `ERR-052` | `spotlight.not_found` | `forwardId` not found | `workspace.ports.remove` |

---

## PTY errors — `ERR-060`–`ERR-065`

| Clause | `data.kind` | Condition | Returned by |
|--------|------------|-----------|-------------|
| `ERR-060` | `pty.not_found` | PTY session not found | `pty.write`, `pty.resize`, `pty.rename`, `pty.close` |

---

## Auth errors — `ERR-070`–`ERR-075`

| Clause | `data.kind` | Condition | Returned by |
|--------|------------|-----------|-------------|
| `ERR-070` | `auth.unknown_token` | Token not found on revoke | `authrelay.revoke` |
| `ERR-071` | `auth.workspace_not_found` | Workspace not found on mint | `authrelay.mint` |
| `ERR-072` | `auth.expired` | Token expired on use | bearer auth check |

---

## CLI exit codes — `ERR-080`–`ERR-082`

**`ERR-080`** — Exit code 0: success.

**`ERR-081`** — Exit code 1: operational error (workspace not found, state conflict, daemon
unreachable, RPC error, etc.).

**`ERR-082`** — Exit code 2: usage error (missing required flags, unknown subcommand, wrong number
of positional args).
