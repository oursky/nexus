# Nexus System Specification — Chapter 04: Workspace RPC Methods

> **Status**: Normative  
> All params and result shapes are exact JSON field names as seen in the wire protocol.

---

## `workspace.create` — `WS-040`–`WS-044`

**`WS-040`** — Request shape:
```json
{
  "spec": {
    "repo":          "string (required)",
    "ref":           "string (required)",
    "workspaceName": "string (required)",
    "projectId":     "string (optional)",
    "agentProfile":  "string (optional)",
    "backend":       "string (optional: 'firecracker' | 'process')",
    "policy": {
      "autoStop":        "bool",
      "autoStopDelay":   "string (duration)",
      "isolationLevel":  "string",
      "maxLifetimeSec":  "int"
    },
    "authBinding":   "any (optional)",
    "configBundle":  "any (optional)"
  }
}
```
Params are nested under `"spec"`. A flat param shape is NOT accepted.

**`WS-041`** — Response shape: `{"workspace": <Workspace>}`.

**`WS-042`** — Pre-conditions:
- `spec.workspaceName` MUST be unique among non-`removed` workspaces — returns `ERR-001` on
  duplicate.
- If `spec.projectId` is set, the project SHOULD exist at creation time (implementation may
  defer validation).

**`WS-043`** — Post-condition: workspace is created in state `created`.

**`WS-044`** — `spec.ref` MUST be non-empty. Ref resolution against the actual repo is deferred
to `workspace.start`; no ref validation occurs at create time.

---

## `workspace.list` — `WS-045`

**`WS-045`** — Request: `{}` (no params). Response: `{"workspaces": [<Workspace>...]}`.
Workspaces in state `removed` MUST NOT appear.

---

## `workspace.info` — `WS-046`–`WS-047`

**`WS-046`** — Request: `{"id": "string"}`. Response: `{"workspace": <Workspace>}`.

**`WS-047`** — If no workspace with the given ID exists (including `removed` state), returns
`ERR-002`.

---

## `workspace.start` — `WS-048`–`WS-050`

**`WS-048`** — Request: `{"id": "string"}`. Response: `{"workspace": <Workspace>}`.

**`WS-049`** — Pre-condition: workspace MUST be in state `created`, `stopped`, or `restored`.
Any other state returns `ERR-011`.

**`WS-050`** — Post-condition: workspace is in state `running`.

---

## `workspace.stop` — `WS-051`–`WS-053`

**`WS-051`** — Request: `{"id": "string"}`. Response: `{"stopped": bool, "workspace"?: <Workspace>}`.
The `workspace` field is optional (`omitempty`).

**`WS-052`** — Pre-condition: workspace MUST be in state `running`. Any other state returns
`ERR-012`.

**`WS-053`** — Post-condition: workspace is in state `stopped`.

---

## `workspace.remove` — `WS-054`–`WS-056`

**`WS-054`** — Request: `{"id": "string"}`. Response: `{"removed": bool}`.

**`WS-055`** — Pre-condition: workspace MUST NOT be in state `running`. Running state returns
`ERR-013`.

**`WS-056`** — Post-condition: workspace is in state `removed`. It MUST NOT appear in subsequent
`workspace.list` results.

---

## `workspace.fork` — `WS-057`–`WS-062`

**`WS-057`** — Request:
```json
{
  "id":                 "string (source workspace ID, required)",
  "childWorkspaceName": "string (optional — daemon generates name if omitted)",
  "childRef":           "string (required)"
}
```
Response: `{"forked": bool, "workspace"?: <Workspace>}`.

**`WS-058`** — `childRef` is REQUIRED. Omitting it or sending an empty string MUST return
`ERR-022`.

**`WS-059`** — Pre-condition: source workspace MUST be in state `running`. Any other state returns
`ERR-011`.

**`WS-060`** — Post-condition: the forked workspace is in state `created`, with `parentWorkspaceId`
set to the source ID and `lineageRootId` computed per `WS-033`.

**`WS-061`** — If `childWorkspaceName` is omitted, the daemon generates a name (implementation-
defined, typically `<parent>-fork`).

**`WS-062`** — The source workspace MUST remain in its current state after a successful fork; it
is not stopped or modified.

---

## `workspace.restore` — `WS-063`–`WS-066`

**`WS-063`** — Request: `{"id": "string"}`. No `snapshotId` parameter — the daemon selects the
most recent snapshot for the workspace. Response: `{"restored": bool, "workspace"?: <Workspace>}`.

**`WS-064`** — Pre-condition: workspace MUST be in state `stopped` or `created`. Returns `ERR-012`
otherwise.

**`WS-065`** — Post-condition: workspace is in state `restored`.

**`WS-066`** — If no snapshot exists for the workspace, returns `ERR-023`.

---

## `workspace.ready` — `WS-067`–`WS-068`

**`WS-067`** — Request: `{"id": "string"}`. Response: `{"ready": bool}`.

**`WS-068`** — If the workspace does not exist, returns `ERR-002`. Returns `{"ready": false}` if
the workspace exists but is not yet ready (readiness is backend-defined).

---

## `workspace.discover-ports` — `WS-069`–`WS-072`

**`WS-069`** — Request: `{"id": "string"}`. Note: the param key is `"id"`, NOT `"workspaceId"`.

**`WS-070`** — Response: a **top-level JSON array** of `DiscoveredPort` objects. The array is NOT
wrapped in an object. Clients MUST decode directly into `[]DiscoveredPort`.

**`WS-071`** — `DiscoveredPort` shape:
```json
{
  "localPort":  "int",
  "remotePort": "int",
  "service":    "string (optional)",
  "protocol":   "string (optional)",
  "source":     "string (optional: 'config' | 'compose')"
}
```

**`WS-072`** — If no ports are discovered, returns an empty array `[]`, not an error.

---

## Workspace domain object — `WS-073`

**`WS-073`** — The `Workspace` object has these JSON fields:
```json
{
  "id":                "string",
  "projectId":         "string",
  "repoId":            "string",
  "repo":              "string",
  "ref":               "string",
  "workspaceName":     "string",
  "agentProfile":      "string",
  "policy":            "<Policy>",
  "state":             "string",
  "rootPath":          "string",
  "authBinding":       "any",
  "tunnelPorts":       "[int]",
  "parentWorkspaceId": "string",
  "lineageRootId":     "string",
  "backend":           "string",
  "configBundle":      "any",
  "created_at":        "RFC3339",
  "updated_at":        "RFC3339"
}
```
