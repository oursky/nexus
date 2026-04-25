# Nexus System Specification — Chapter 04: PTY RPC Methods

> **Status**: Normative

---

## `pty.create` — `PTY-010`–`PTY-016`

**`PTY-010`** — Request:
```json
{
  "workspaceId": "string (required)",
  "name":        "string (optional — session label for display)",
  "shell":       "string (optional — path to shell binary; defaults to $SHELL or /bin/sh)",
  "args":        ["string"] "(optional — passed to shell; defaults to ['-l'] for interactive)",
  "workDir":     "string (optional — working directory inside workspace; defaults to /workspace)",
  "cols":        "int (optional, default 80)",
  "rows":        "int (optional, default 24)"
}
```

**`PTY-011`** — Response: `SessionInfo` object (see `PTY-030`).

**`PTY-012`** — Pre-condition: `workspaceId` is REQUIRED. Empty value returns invalid params.

**`PTY-013`** — If `cols` or `rows` are zero or absent, defaults of 80 and 24 are applied
respectively.

**`PTY-014`** — If `args` is empty or absent, the shell is launched interactively with `-l`.
If `args` is provided (e.g. `["-c", "echo hello"]`), the shell executes that command and exits.

**`PTY-015`** — In libkrun backend mode, the PTY session is created on the guest via the
vsock agent connection. The agent protocol is internal (see `PTY-008` in `01-concepts.md`).

**`PTY-016`** — Post-condition: session appears in `pty.list` for the given workspace.

---

## `pty.list` — `PTY-017`–`PTY-018`

**`PTY-017`** — Request: `{"workspaceId": "string (required)"}`.

**`PTY-018`** — Response: `{"sessions": [<SessionInfo>...]}`. Key is `"sessions"` (not a bare
array). Only live sessions for the given workspace are returned. Terminated sessions MUST NOT
appear.

---

## `pty.write` — `PTY-019`–`PTY-021`

**`PTY-019`** — Request: `{"sessionId": "string", "data": "string"}`.

**`PTY-020`** — `data` is a raw string (UTF-8 text or control characters). It is NOT base64-
encoded.

**`PTY-021`** — Response: `{}` on success. Returns `ERR-061` if session is not found.

---

## `pty.resize` — `PTY-022`–`PTY-023`

**`PTY-022`** — Request: `{"sessionId": "string", "cols": int, "rows": int}`.

**`PTY-023`** — Response: `{"ok": true}`. Returns `ERR-061` if session is not found. Applies new
dimensions to the underlying PTY immediately.

---

## `pty.rename` — `PTY-024`–`PTY-025`

**`PTY-024`** — Request: `{"sessionId": "string", "name": "string"}`.

**`PTY-025`** — Response: `{"ok": true}`. Returns `ERR-061` if session is not found.

---

## `pty.close` — `PTY-026`–`PTY-028`

**`PTY-026`** — Request: `{"sessionId": "string"}`.

**`PTY-027`** — Terminates the PTY process (kills if local; sends `shell.close` if libkrun VM),
closes any underlying connections, and removes the session from the registry.

**`PTY-028`** — Response: `{"ok": true}`. Returns `ERR-061` if session is not found.

---

## Push notifications — `PTY-029`

**`PTY-029`** — The daemon sends two types of push notifications for PTY sessions (WebSocket/mux
connections only):

| Method | Params | Notes |
|--------|--------|-------|
| `pty.data` | `{"sessionId": "string", "data": "string"}` | Raw terminal output; NOT base64 |
| `pty.exit` | `{"sessionId": "string", "exitCode": int}` | Session exit code |

---

## SessionInfo domain object — `PTY-030`

**`PTY-030`** — The `SessionInfo` object has these JSON fields:
```json
{
  "id":          "string (pty-<nanoseconds>)",
  "workspaceId": "string",
  "name":        "string",
  "shell":       "string",
  "workDir":     "string",
  "cols":        "int",
  "rows":        "int",
  "createdAt":   "RFC3339",
  "isRemote":    "bool (true for libkrun vsock sessions)"
}
```
