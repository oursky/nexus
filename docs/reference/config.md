# Config Reference

This document lists every configuration file used by Nexus, with their locations, formats, and purposes. It is reconciled against the current implementation.

---

## Project-level config

### `Nexusfile`

| | |
|---|---|
| **Location** | `<projectRoot>/Nexusfile` |
| **Format** | TOML |
| **Purpose** | Intent-first app declaration (services, domains, resources, local dev lifecycle) |
| **Required** | No — standard projects auto-detect configuration from `package.json`, `Procfile`, etc. |

**Canonical shape:**

```toml
name = "acme"

[build]
image = "node:20-alpine"           # base image or well-known shorthand
init = ["npm ci"]                  # one-time setup commands

[dev]
up = "npm run dev"                 # local dev start command
down = "pkill -f next"             # local dev stop command (optional)
port = 3000                        # primary port for auto-forwarding

[[services]]
name = "web"
source = "."
start = "npm start"
port = 3000

[[domains]]
host = "app.example.com"
service = "web"
path = "/"

[[resources]]
name = "db"
type = "postgres"
mode = "managed"
bind = ["web"]
as = "DATABASE_URL"

[release]
command = "npm run migrate"
```

**Field summary:**

| Section | Key | Description |
|---------|-----|-------------|
| top | `name` | App identifier. Auto-detected from `package.json` / `go.mod` / directory name if omitted. |
| `[build]` | `image` | Base image reference or well-known name (`node`, `go`, `python`, `rust`, `ruby`). Mutually exclusive with `dockerfile`. |
| `[build]` | `dockerfile` | Path to Dockerfile. Mutually exclusive with `image`. |
| `[build]` | `init` | One-time commands run during base-layer build. Changing this invalidates the base image cache. |
| `[dev]` | `up` | Local dev start command. Auto-detected if omitted. |
| `[dev]` | `down` | Local dev stop command. Optional. |
| `[dev]` | `port` | Primary local port for auto-forwarding. Auto-detected if omitted. |
| `services[]` | `name` | Unique service key. |
| `services[]` | `source` | Working directory relative to project root (default: `.`). |
| `services[]` | `start` | Process command. Auto-detected from `Procfile` or `package.json` if omitted. |
| `services[]` | `port` | Listening port. Required if targeted by a domain. |
| `domains[]` | `host` | Public domain intent. |
| `domains[]` | `service` | Target service name. |
| `domains[]` | `path` | Route path prefix (default: `/`). |
| `resources[]` | `name` | Unique resource key. |
| `resources[]` | `type` | Resource type. `postgres` is the only supported type in v1. |
| `resources[]` | `mode` | `managed` (platform-provisioned) or `local` (dev-only). |
| `resources[]` | `bind` | Services that receive the resource connection. |
| `resources[]` | `as` | Env var name for the connection string. |
| `[release]` | `command` | One-off command run before traffic cutover (e.g. migrations). |

**Legacy format:** The old `vm.profile`, `vm.image`, `dev.init`, `dev.volumes`, and `manifest` fields are no longer accepted. Run `nexus config migrate` to update legacy Nexusfiles.

**Loaded by:**
- `internal/infra/config.LoadNexusfile` — parses TOML
- `internal/infra/config.ResolveNexusfile` — parses + auto-detects missing fields + validates
- Consumed by workspace spawn (base image selection) and deploy plan resolution

---

## User-level config

### Node config

| | |
|---|---|
| **Location** | `$XDG_CONFIG_HOME/nexus/node.json` (fallback: `~/.nexus/node.json`) |
| **Format** | JSON |
| **Purpose** | Node identity & capability advertisement |
| **Required** | No — defaults to `{"version": 1}` when missing |

**Schema:**

```json
{
  "$schema": "...",
  "version": 1,
  "node": {
    "name": "my-builder",
    "tags": ["gpu", "ci"]
  },
  "capabilities": {
    "provide": ["runtime.libkrun", "toolchain.xcodebuild"]
  },
  "compatibility": {
    "minimumDaemonVersion": "0.1.0"
  }
}
```

- `node.name` / `node.tags` — human-readable identity
- `capabilities.provide` — explicit capability advertisements (e.g. `"runtime.libkrun"`)
- `compatibility.minimumDaemonVersion` — semver-like minimum daemon version

**Loaded by:** `internal/infra/config.LoadNodeConfig`

---

### CLI profile

| | |
|---|---|
| **Location** | `~/.config/nexus/profiles/default.json` (respects `$XDG_CONFIG_HOME`) |
| **Format** | JSON |
| **Purpose** | Daemon connection profile |
| **Required** | Yes — created by `nexus daemon connect` |

**Schema:**

```json
{
  "name": "default",
  "host": "user@remote-host",
  "port": 7777,
  "sshPort": 22
}
```

- The auth **token** is stored separately in the OS keychain (macOS Keychain / Linux SecretService / headless file fallback)
- On headless Linux without D-Bus, the token fallback path is `~/.config/nexus/daemon-token`

**Loaded by:** `internal/infra/cli/profile.LoadDefault`

---

### API keys

| | |
|---|---|
| **Location** | `~/.config/nexus/api-keys.env` |
| **Format** | Shell env (`KEY=value`, `#` comments ignored) |
| **Purpose** | Persist AI/LLM API keys for injection into libkrun VM guests |
| **Required** | No |

Example:

```bash
# ~/.config/nexus/api-keys.env
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
```

Keys are overlayed on top of the host's environment variables when building the guest config drive.

**Loaded by:** `internal/infra/runtime/libkrun/host_config_drive.go`

---

## Removed configs

The following project-level `.nexus/` config files have been removed. Nexus now uses `Nexusfile` for project-level configuration.

| File | Status |
|------|--------|
| `.nexus/workspace.json` | **Removed** — all project-level config now lives in `Nexusfile` |
| `.nexus/lifecycle.json` | **Removed** — was already ignored in code |
| `.nexus/lifecycles/*.sh` | **Removed** — lifecycle scripts no longer managed by Nexus |
| `.nexus/probe/*.sh` | **Removed** — doctor probe discovery removed |
| `.nexus/check/*.sh` | **Removed** — doctor check discovery removed |
| `.nexus/run/nexus-init-env` | **Removed** — runtime backend hint no longer written |

---

## Internal / runtime files

These files are managed automatically and are not user-editable configuration:

| Path | Purpose |
|------|---------|
| `~/.nexus/node.db` | SQLite node-level metadata (fallback: `$XDG_STATE_HOME/nexus/node.db`) |
| `~/.config/nexus/run/token` | Per-user daemon autostart token (fallback: `$XDG_RUNTIME_DIR/nexus/token`) |
