# Operations playbook

Short reference for **latency**, **isolation concepts**, and **paths**.

## Doctor vs backend

- **`nexus doctor`** runs from CWD (no flags required to specify project root). There is no top-level `--timeout`; probes use internal timeouts.
- On startup, the CLI prints **`doctor: runtime backend=…`** so you know whether the runtime is **firecracker** or **process** (or other supported backend).
- **Firecracker, cold VM:** the first run can take **several minutes** (guest bootstrap, Docker/tooling) before your `.nexus/probe` scripts run. Silence is often normal.
- **Process sandbox** (fallback when VM is unavailable): usually **much faster** for the same project.
- Predicting backend: see `nexus create --backend …` and host capabilities. See [Workspace config](../reference/workspace-config.md).

## Isolation: fork vs workspace vs git worktree

| Mechanism | What it isolates | Typical use |
|-----------|------------------|-------------|
| **Git worktree** | Second checkout + branch on the **same machine** | Parallel features without branch switching in one tree. |
| **New Nexus workspace (`create`)** | Separate workspace id, runtime, and often VM | Remote execution, different repos/refs, clean processes. |
| **`fork`** | Child workspace derived from a parent (product semantics) | Experiment from a snapshot; check current docs for auth-bundle and metadata. |

Worktrees do **not** replace Nexus workspaces for remote sandboxes; they solve different problems.

## Remote Daemon (`nexus daemon start` on Linux)

Run the daemon on a remote Linux host so that a local `nexus` client can connect to it over the network.
The `nexus` binary is unified — it serves both CLI and daemon roles.

### Prerequisites

- Linux x86-64
- Go 1.22+ (to build from source) **or** a pre-built `nexus` binary
- Port 7777 reachable from the client, or an SSH tunnel

### Build from source

```bash
git clone https://github.com/inizio/nexus ~/magic/nexus
cd ~/magic/nexus/packages/nexus
go build -tags nodbus -o ~/magic/bin/nexus ./cmd/nexus
export PATH="$HOME/magic/bin:$PATH"
```

### Bearer token

The daemon auto-generates and persists a token on first start when `--network` is active and
no `--token` flag is provided. To retrieve or regenerate it:

```bash
nexus daemon token
```

You can also pass a static token explicitly via `--token <value>` or the
`NEXUS_DAEMON_TOKEN` environment variable.

### Start the daemon with a network listener

**Direct TCP (Tailscale / VPN / firewall rule for port 7777):**

```bash
nexus daemon start --network --bind 0.0.0.0 --port 7777 --tls auto
```

Token is auto-generated and stored; print it with `nexus daemon token`.

**Loopback + SSH tunnel (no firewall change needed, no TLS required):**

```bash
# On the Linux host:
nexus daemon start --network --bind 127.0.0.1 --port 7777

# On the client machine, forward the remote port locally:
ssh -N -L 7777:127.0.0.1:7777 user@remote-host
```

### TLS (non-loopback deployments)

Use `--tls required` with a certificate and key for direct public exposure:

```bash
nexus daemon start --network --bind 0.0.0.0 --port 7777 --token <token> \
  --tls required --tls-cert /etc/nexus/cert.pem --tls-key /etc/nexus/key.pem
```

Use `--tls auto` for a self-signed certificate (clients must accept the cert):

```bash
nexus daemon start --network --bind 0.0.0.0 --port 7777 --tls auto
```

The default (`--tls off`) sends traffic in plaintext — safe only over loopback or SSH tunnels.

### Systemd user unit (persistent service)

Create `~/.config/systemd/user/nexusd.service`:

```ini
[Unit]
Description=Nexus Daemon
After=network.target

[Service]
ExecStart=/home/<user>/magic/bin/nexus daemon start \
  --network \
  --bind 0.0.0.0 \
  --port 7777 \
  --tls auto
Restart=on-failure
RestartSec=5s
WorkingDirectory=/home/<user>/magic

[Install]
WantedBy=default.target
```

Enable and start (user unit — no sudo required):

```bash
systemctl --user daemon-reload
systemctl --user enable --now nexusd
systemctl --user status nexusd
```

The bearer token is auto-generated on first start and persisted to
`~/.config/nexus/daemon-token` (or the OS keyring on supported platforms).
Print it with:

```bash
nexus daemon token
```

### Firewall

If binding to `0.0.0.0`, open port 7777:

```bash
# ufw
sudo ufw allow 7777/tcp

# firewalld
sudo firewall-cmd --permanent --add-port=7777/tcp && sudo firewall-cmd --reload

# iptables
sudo iptables -A INPUT -p tcp --dport 7777 -j ACCEPT
```

If using Tailscale or loopback + SSH tunnel, no firewall change is required.

### Health and version checks

```bash
curl http://localhost:7777/healthz
# {"status":"ok"}

curl http://localhost:7777/version
# {"version":"dev"}
```

For TLS deployments, use `https://` and pass `-k` (or `--cacert`) as appropriate.

### Validation checklist

- [ ] `systemctl --user status nexusd` shows `active (running)`
- [ ] `curl http://localhost:7777/healthz` returns `{"status":"ok"}`
- [ ] `curl http://localhost:7777/version` returns a version object
- [ ] `nexus daemon token` prints the bearer token
- [ ] Client `nexus list` connects without authentication errors

## Related

- [Host auth bundle](../reference/host-auth-bundle.md)
- [CLI reference](../reference/cli.md)
- [Installation](installation.md)
