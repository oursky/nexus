# Nexus System Specification — Chapter 04: Auth Relay RPC Methods

> **Status**: Normative

---

## `authrelay.mint` — `AUTH-010`–`AUTH-015`

**`AUTH-010`** — Request:
```json
{
  "workspaceId": "string (required)",
  "binding":     "string (required)",
  "ttlSeconds":  "int (optional)"
}
```

**`AUTH-011`** — Response:
```json
{
  "token": "string"
}
```

**`AUTH-012`** — `ttlSeconds` controls token lifetime in seconds. If omitted or zero, the
implementation chooses a default.

**`AUTH-013`** — `workspaceId` MUST reference an existing workspace. Returns `ERR-002` if not found.

**`AUTH-014`** — `binding` MUST name a key present in the workspace's `authBinding` map. Returns
`ERR-002` if the binding is not found.

**`AUTH-015`** — The issued token is opaque. Its internal format is implementation-defined. It is
valid only on the daemon instance that issued it.

**`AUTH-016`** — The auth relay token is distinct from the daemon bearer token used to authenticate
WebSocket connections. They serve different purposes and MUST NOT be confused.

---

## `authrelay.revoke` — `AUTH-017`–`AUTH-019`

**`AUTH-017`** — Request: `{"token": "string"}`.

**`AUTH-018`** — Response: `{"revoked": true}` on success. Revoking an unknown or already-revoked
or already-expired token also returns `{"revoked": true}` with no error.

**`AUTH-019`** — Revoke is idempotent: calling it multiple times with the same token always
succeeds.
