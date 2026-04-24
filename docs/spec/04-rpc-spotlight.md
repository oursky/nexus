# Nexus System Specification — Chapter 04: Spotlight RPC Methods

> **Status**: Normative

---

## Handler ownership — `SPOT-010`–`SPOT-012`

**`SPOT-010`** — The spotlight handler owns and exclusively registers the following methods:
`spotlight.start`, `spotlight.list`, `spotlight.stop`,
`workspace.ports.list`, `workspace.ports.add`, `workspace.ports.remove`.

**`SPOT-011`** — The workspace handler does NOT register any `workspace.ports.*` or
`workspace.tunnels.*` methods. There is no registration conflict on `main`.

**`SPOT-012`** — `spotlight.*` and `workspace.ports.*` are two naming surfaces for related but
semantically distinct operations:
- `spotlight.start` / `spotlight.stop`: operate at workspace granularity (start/stop all
  forwards for a workspace)
- `workspace.ports.add` / `workspace.ports.remove`: operate at individual forward granularity

---

## `spotlight.start` — `SPOT-013`–`SPOT-016`

**`SPOT-013`** — Request:
```json
{
  "workspaceId": "string (required)",
  "spec": {
    "localPort":  "int",
    "remotePort": "int",
    "protocol":   "string (optional)",
    "source":     "string (optional)"
  }
}
```

**`SPOT-014`** — Response: `{"forward": <Forward>}`. Creates exactly ONE port forward.

**`SPOT-015`** — This method creates a single forward. The bulk "discover and forward all ports"
behaviour is implemented in the CLI (`nexus spotlight start`), which calls `workspace.discover-ports`
then calls `spotlight.start` once per discovered port in a loop.

**`SPOT-016`** — Pre-condition: workspace MUST exist. If not found, returns `ERR-002`.

---

## `spotlight.list` — `SPOT-017`–`SPOT-019`

**`SPOT-017`** — Request: `{"workspaceId": "string (required)"}`.

**`SPOT-018`** — Response: `{"forwards": [<Forward>...]}`. Returns all active forwards for the
given workspace. If no forwards, returns `{"forwards": []}`.

**`SPOT-019`** — `workspaceId` is REQUIRED. An empty value returns `ERR-`invalid-params`.

---

## `spotlight.stop` — `SPOT-020`–`SPOT-023`

**`SPOT-020`** — Request: `{"workspaceId": "string (required)"}`.

**`SPOT-021`** — Response: `{"closed": bool}`.

**`SPOT-022`** — This method closes **ALL** active forwards for the workspace atomically. It calls
`svc.StopWorkspaceSpotlight(ctx, workspaceID)`. It is NOT a per-forward operation.

**`SPOT-023`** — If the workspace has no active forwards, this is a no-op and returns
`{"closed": true}`.

---

## `workspace.ports.list` — `SPOT-024`–`SPOT-026`

**`SPOT-024`** — Request: `{"workspaceId": "string (required)"}`.

**`SPOT-025`** — Response: `{"forwards": [<Forward>...]}`. Identical to `spotlight.list`. Returns
all active forwards for the given workspace.

**`SPOT-026`** — `workspace.ports.list` and `spotlight.list` share the same handler implementation.
They are interchangeable for reads.

---

## `workspace.ports.add` — `SPOT-027`–`SPOT-030`

**`SPOT-027`** — Request:
```json
{
  "workspaceId": "string (required)",
  "spec": {
    "localPort":  "int (required)",
    "remotePort": "int (required)",
    "protocol":   "string (optional, default 'tcp')"
  }
}
```

**`SPOT-028`** — Response: `{"forward": <Forward>}`.

**`SPOT-029`** — `workspace.ports.add` and `spotlight.start` share the same handler implementation
and are interchangeable for creating a single forward.

**`SPOT-030`** — Pre-condition: workspace MUST exist. Returns `ERR-002` if not found.

---

## `workspace.ports.remove` — `SPOT-031`–`SPOT-034`

**`SPOT-031`** — Request:
```json
{
  "workspaceId": "string (required)",
  "forwardId":   "string (required)"
}
```

**`SPOT-032`** — Response: `{"closed": bool}`.

**`SPOT-033`** — Closes the single forward identified by `forwardId`. This is a per-forward
operation, unlike `spotlight.stop` which is per-workspace.

**`SPOT-034`** — `forwardId` is REQUIRED. An empty value returns invalid params error. If the
forward is not found, returns `ERR-052`.

---

## Forward domain object — `SPOT-035`

**`SPOT-035`** — The `Forward` object has these JSON fields:
```json
{
  "id":          "string (spot-<nanoseconds>)",
  "workspaceId": "string",
  "localPort":   "int",
  "remotePort":  "int",
  "targetHost":  "string (Firecracker resolved host, or '' for process backend)",
  "protocol":    "string",
  "state":       "string ('active' | 'closed')",
  "created_at":  "RFC3339"
}
```
