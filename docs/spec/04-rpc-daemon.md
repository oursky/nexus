# Nexus System Specification — Chapter 04: Daemon RPC Methods

> **Status**: Normative

---

## `node.info` — `DAEMON-020`–`DAEMON-025`

**`DAEMON-020`** — Request: `{}` (no params required).

**`DAEMON-021`** — Response:
```json
{
  "node": {
    "name": "string",
    "tags": ["string", "..."]
  },
  "capabilities": [
    {
      "name":      "string",
      "available": "bool"
    }
  ]
}
```

**`DAEMON-022`** — `capabilities` always includes at least one entry (e.g. `"runtime.libkrun"`).

**`DAEMON-023`** — `capabilities` includes `"runtime.libkrun"` with `available: true` when the
daemon started successfully (libkrun is required; there is no process/sandbox fallback).

**`DAEMON-024`** — `node.info` is used as the readiness probe. It MUST succeed as soon as the
daemon socket is bound and RPC handlers are registered, even before all background services are
fully initialized.

**`DAEMON-025`** — Clients SHOULD call `node.info` after establishing a connection to verify
node identity and available capabilities before calling capability-specific methods.

---

## `daemon.log.tail` — `DAEMON-026`–`DAEMON-028`

**`DAEMON-026`** — Request:
```json
{
  "lines": "int (optional, default 200)"
}
```

**`DAEMON-027`** — Response:
```json
{
  "lines": ["string", "..."],
  "path":  "string"
}
```

**`DAEMON-028`** — Returns up to `lines` trailing log lines from the daemon log file. If the log
file does not exist or is empty, `lines` is an empty array and `path` is the expected log file path
(or empty if no log path is configured).
