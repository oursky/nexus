# Nexus System Specification — Chapter 04: Auth Relay RPC Methods

> **Status**: Normative

---

## `authrelay.mint` — `AUTH-010`–`AUTH-015`

**`AUTH-010`** — Request:
```json
{
  "workspaceId": "string (required)",
  "duration":    "string (optional — Go duration string, e.g. '1h', '30m'; default: 1h)"
}
```

**`AUTH-011`** — Response:
```json
{
  "token":     "string",
  "expiresAt": "RFC3339 timestamp"
}
```

**`AUTH-012`** — Default duration is 1 hour if not specified.

**`AUTH-013`** — `workspaceId` MUST reference an existing workspace. Returns `ERR-002` if not found.

**`AUTH-014`** — The issued token is opaque. Its internal format is implementation-defined. It is
valid only on the daemon instance that issued it.

**`AUTH-015`** — The auth relay token is distinct from the daemon bearer token used to authenticate
WebSocket connections. They serve different purposes and MUST NOT be confused.

---

## `authrelay.revoke` — `AUTH-016`–`AUTH-018`

**`AUTH-016`** — Request: `{"token": "string"}`.

**`AUTH-017`** — Response: `{}` on success.

**`AUTH-018`** — Revoking an unknown or already-revoked or already-expired token MUST return
`ERR-030`. Revoke is NOT idempotent.
