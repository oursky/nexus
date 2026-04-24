# Nexus System Specification — Chapter 03: RPC Protocol

> **Status**: Normative

---

## Unix socket transport — `RPC-001`–`RPC-007`

**`RPC-001`** — The daemon MUST bind a Unix domain socket at the configured socket path before
emitting any readiness signal.

**`RPC-002`** — Each client connection to the Unix socket is independent. The daemon MUST handle
concurrent connections without serializing requests across connections.

**`RPC-003`** — Messages are newline-delimited: a complete JSON-RPC 2.0 object terminated by `\n`
constitutes exactly one message. The daemon MUST read full lines before attempting JSON parsing.

**`RPC-004`** — The Unix socket transport does NOT require authentication. Any process with
read/write access to the socket file path may connect and issue requests.

**`RPC-005`** — The daemon MUST remove the Unix socket file on clean shutdown.

**`RPC-006`** — If a stale socket file exists at daemon startup, the daemon MUST remove it before
binding the new socket.

**`RPC-007`** — The Unix socket transport does NOT deliver server-push notifications. Push
notifications (`pty.data`, `pty.exit`) are only delivered on WebSocket (mux) connections.

---

## WebSocket / HTTP transport — `RPC-008`–`RPC-016`

**`RPC-008`** — When `--network` is enabled (default: true), the daemon MUST serve HTTP on
`<bind>:<port>` (default `127.0.0.1:7777`).

**`RPC-009`** — `GET /healthz` MUST return HTTP 200 with JSON body `{"status": "ok"}`.

**`RPC-010`** — `GET /version` MUST return HTTP 200 with JSON body `{"version": "<version>"}`.

**`RPC-011`** — `GET /` with `Upgrade: websocket` header upgrades to a WebSocket connection for
JSON-RPC. The daemon MUST reject non-WebSocket GET requests to `/` with HTTP 404.

**`RPC-012`** — When a bearer token is configured, the daemon MUST reject WebSocket upgrade
requests that do not include `Authorization: Bearer <token>` with HTTP 401.

**`RPC-013`** — WebSocket messages are JSON-RPC 2.0 objects, one per WebSocket text frame.

**`RPC-014`** — TLS modes: `off` (default, unencrypted), `auto` (self-signed or ACME),
`required` (explicit cert/key via `--tls-cert` / `--tls-key`).

**`RPC-015`** — If `--bind` is not a loopback address (`127.0.0.1` or `::1`), TLS MUST be `auto`
or `required`. The daemon MUST refuse to start with `tls: off` on a non-loopback address.

**`RPC-016`** — Token resolution order: `--token` flag → `NEXUS_DAEMON_TOKEN` env → auto-generated
via tokenstore (when network is enabled and no other source). The auto-generated token is
persisted so the CLI can retrieve it.

---

## JSON-RPC 2.0 contract — `RPC-017`–`RPC-025`

**`RPC-017`** — Every request MUST include: `"jsonrpc": "2.0"`, `"method": <string>`,
`"id": <string|int>`, `"params": <object|null>`.

**`RPC-018`** — Every response MUST include: `"jsonrpc": "2.0"`, `"id": <matching request id>`,
and either `"result": <value>` (success) or `"error": <object>` (failure). Both MUST NOT be
present simultaneously.

**`RPC-019`** — Error objects MUST contain: `"code": <int>`, `"message": <string>`. The `"data"`
field is optional. When present, `"data"` MUST contain `"kind": <string>` — a machine-readable
error identifier (see `06-error-taxonomy.md`).

**`RPC-020`** — Standard JSON-RPC error codes:
- `-32700`: Parse error (malformed JSON)
- `-32600`: Invalid Request (missing required fields)
- `-32601`: Method Not Found
- `-32602`: Invalid Params (schema validation failure)
- `-32000`: Application error (workspace not found, state conflict, etc.)

**`RPC-021`** — The daemon MUST NOT return `null` as a successful result for methods that have no
meaningful return value; it MUST return `{}` or a typed empty object.

**`RPC-022`** — Server-push notifications are JSON objects with `"jsonrpc": "2.0"`,
`"method": <string>`, `"params": <object>`, and NO `"id"` field.

**`RPC-023`** — Active push notification methods:
- `pty.data` — params: `{"sessionId": string, "data": string}`. `data` is raw terminal output
  (UTF-8 text or binary-safe string), NOT base64-encoded.
- `pty.exit` — params: `{"sessionId": string, "exitCode": int}`

**`RPC-024`** — Push notifications MUST only be delivered on WebSocket connections that have a
`Notifier` attached. Unix socket connections MUST NOT receive push notifications.

**`RPC-025`** — The daemon MUST handle concurrent in-flight requests from the same connection.
Request ordering within a connection is not guaranteed in the response direction.

---

## Mux connections — `RPC-026`–`RPC-029`

**`RPC-026`** — The CLI uses a multiplexed WebSocket connection (`MuxConn`) that combines RPC
calls and push notification subscriptions on a single WebSocket connection.

**`RPC-027`** — The mux layer is an internal implementation detail of the CLI. External clients
MUST use plain WebSocket + JSON-RPC 2.0 directly.

**`RPC-028`** — On mux connections, `pty.data` and `pty.exit` notifications are received via
channel subscriptions (`conn.Subscribe("pty.data")`), not as bare WebSocket frames.

**`RPC-029`** — `workspace.discover-ports` returns a top-level JSON array (not wrapped in an
object). Clients MUST decode the result directly into `[]DiscoveredPort`, not into a struct with
a named field.
