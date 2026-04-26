# Nexus System Specification — Chapter 04: Daemon RPC Methods

> **Status**: Normative

---

## `node.info` — `DAEMON-020`–`DAEMON-025`

**`DAEMON-020`** — Request: `{}` (no params required).

**`DAEMON-021`** — Response:
```json
{
  "version":      "string",
  "capabilities": ["string", "..."]
}
```

**`DAEMON-022`** — `capabilities` always includes `"runtime.process"`.

**`DAEMON-023`** — `capabilities` includes `"runtime.libkrun"` when the daemon was started
with the libkrun driver (i.e., without `--sandbox` on Linux).

**`DAEMON-024`** — `node.info` is used as the readiness probe. It MUST succeed as soon as the
daemon socket is bound and RPC handlers are registered, even before all background services are
fully initialized.

**`DAEMON-025`** — Clients SHOULD call `node.info` after establishing a connection to verify
daemon version and available capabilities before calling capability-specific methods.
