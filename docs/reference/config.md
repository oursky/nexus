# Config Reference

This document lists every configuration file used by Nexus, with their locations, formats, and purposes. It is reconciled against the current implementation.

---

## Project-level config

### `Nexusfile`

| | |
|---|---|
| **Location** | `<projectRoot>/Nexusfile` |
| **Format** | TOML (legacy JSON accepted for backward compatibility) |
| **Purpose** | User-facing project configuration |
| **Required** | No — missing file falls back to defaults |

**Supported fields:**

```toml
[vm]
profile = "default"   # "minimal" or "default"
image = ""            # reserved for future prebuilt image selection
```

- `vm.profile` controls in-guest tool installation behavior.
  - `"minimal"` — minimal tooling
  - `"default"` — standard tooling (default when omitted)
- `vm.image` is reserved for future use.

**Loaded by:**
- `internal/infra/config.LoadNexusfile`
- Consumed by the libkrun VM driver at workspace spawn time

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
