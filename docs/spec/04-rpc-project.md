# Nexus System Specification — Chapter 04: Project RPC Methods

> **Status**: Normative

---

## `project.create` — `PRJ-010`–`PRJ-014`

**`PRJ-010`** — Request:
```json
{
  "name":     "string (required)",
  "repoUrl":  "string (required)",
  "rootPath": "string (optional)"
}
```

**`PRJ-011`** — Response: the created `Project` object (exact shape: `PRJ-030`).

**`PRJ-012`** — `name` MUST be unique among all projects. Duplicate returns `ERR-040`.

**`PRJ-013`** — `repoUrl` MUST be non-empty. Empty value returns `ERR-041`.

**`PRJ-014`** — Post-condition: project is visible in `project.list`.

---

## `project.list` — `PRJ-015`

**`PRJ-015`** — Request: `{}`. Response: array of `Project` objects. Removed projects MUST NOT
appear. The exact result field name is implementation-defined (verify against actual daemon
response; expected `{"projects": [...]}` or bare array).

---

## `project.get` — `PRJ-016`–`PRJ-017`

**`PRJ-016`** — Request: `{"id": "string"}`. Response: `Project` object.

**`PRJ-017`** — If no project with the given ID exists, returns `ERR-042`.

---

## `project.remove` — `PRJ-018`–`PRJ-020`

**`PRJ-018`** — Request: `{"id": "string"}`. Response: `{}`.

**`PRJ-019`** — If no project with the given ID exists, returns `ERR-042`.

**`PRJ-020`** — Post-condition: project no longer appears in `project.list`. Associated workspaces
retain their `projectId` field as a dangling reference; this is not treated as an error.

---

## Project domain object — `PRJ-030`

**`PRJ-030`** — The `Project` object has these JSON fields:
```json
{
  "id":         "string",
  "name":       "string",
  "repoUrl":    "string",
  "rootPath":   "string",
  "config": {
    "defaultBackend": "string",
    "defaultRef":     "string"
  },
  "created_at": "RFC3339",
  "updated_at": "RFC3339"
}
```
