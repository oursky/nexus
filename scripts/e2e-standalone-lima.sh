#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NEXUS_MOD="$ROOT/packages/nexus"
LIMA_INSTANCE="${NEXUS_LIMA_INSTANCE:-nexus-libkrun}"
AUTO_BOOTSTRAP="${NEXUS_LIMA_AUTO_BOOTSTRAP:-1}"

if ! command -v limactl >/dev/null 2>&1; then
  echo "e2e-standalone-lima: limactl is required" >&2
  exit 1
fi

if ! limactl list 2>/dev/null | grep -q "$LIMA_INSTANCE"; then
  if [[ "$AUTO_BOOTSTRAP" != "1" ]]; then
    echo "e2e-standalone-lima: Lima instance '$LIMA_INSTANCE' not found" >&2
    echo "set NEXUS_LIMA_AUTO_BOOTSTRAP=1 to auto-create it" >&2
    exit 1
  fi
  echo "e2e-standalone-lima: creating Lima instance '$LIMA_INSTANCE'" >&2
  limactl start --name="$LIMA_INSTANCE" -y template://ubuntu >/dev/null
fi

if ! limactl list 2>/dev/null | grep "$LIMA_INSTANCE" | grep -q "Running"; then
  if [[ "$AUTO_BOOTSTRAP" != "1" ]]; then
    echo "e2e-standalone-lima: Lima instance '$LIMA_INSTANCE' is not running" >&2
    echo "set NEXUS_LIMA_AUTO_BOOTSTRAP=1 to auto-start it" >&2
    exit 1
  fi
  echo "e2e-standalone-lima: starting Lima instance '$LIMA_INSTANCE'" >&2
  limactl start --name="$LIMA_INSTANCE" -y >/dev/null
fi

TMP_BIN="$(mktemp "${TMPDIR:-/tmp}/nexus-lima-e2e.XXXXXX")"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/nexus-lima-standalone.XXXXXX")"
STATE_DIR="$WORK_DIR/state"
SOCKET_PATH="$STATE_DIR/nexusd.sock"
DB_PATH="$STATE_DIR/nexus.db"
trap 'rm -f "$TMP_BIN"; rm -rf "$WORK_DIR"' EXIT

run_nexus() {
  XDG_STATE_HOME="$STATE_DIR" "$TMP_BIN" "$@"
}

echo "== build nexus CLI via mise =="
(cd "$NEXUS_MOD" && mise exec -- go build -o "$TMP_BIN" ./cmd/nexus)

echo "== start isolated daemon (process driver) =="
mkdir -p "$STATE_DIR"
run_nexus daemon start --foreground --network=false --driver process --socket "$SOCKET_PATH" --db "$DB_PATH" >"$STATE_DIR/daemon.log" 2>&1 &
DAEMON_PID=$!
for _ in $(seq 1 30); do
  if run_nexus daemon status >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! run_nexus daemon status >/dev/null 2>&1; then
  echo "e2e-standalone-lima: daemon failed to become ready" >&2
  tail -n 80 "$STATE_DIR/daemon.log" >&2 || true
  exit 1
fi
trap 'kill "$DAEMON_PID" >/dev/null 2>&1 || true; rm -f "$TMP_BIN"; rm -rf "$WORK_DIR"' EXIT

REPO_DIR="$WORK_DIR/repo"
mkdir -p "$REPO_DIR"
cat >"$REPO_DIR/Nexusfile" <<'EOF'
name = "standalone-proof"

[workspace]
init = "echo init > /workspace/.init-ran"
up = "echo up > /workspace/.up-ran"
down = "echo down > /workspace/.down-ran"

[[services]]
name = "api"
start = "echo deploy-start > /workspace/.services-start-ran"
EOF

(
  cd "$REPO_DIR"
  git init -q
  git config user.email "e2e@nexus.test"
  git config user.name "Nexus E2E"
  git add Nexusfile
  git commit -q -m init
)

echo "== create/start workspace =="
create_out="$(cd "$REPO_DIR" && run_nexus workspace create --name standalone-proof --repo "$REPO_DIR" --backend process 2>&1)"
printf '%s\n' "$create_out"
ws_id="$(printf '%s\n' "$create_out" | perl -ne 'print "$1\n" if /\(id: ([^)]+)\)/' | head -n1)"
if [[ -z "$ws_id" ]]; then
  echo "e2e-standalone-lima: failed to parse workspace id" >&2
  exit 1
fi
trap 'run_nexus workspace remove "$ws_id" >/dev/null 2>&1 || true; kill "$DAEMON_PID" >/dev/null 2>&1 || true; rm -f "$TMP_BIN"; rm -rf "$WORK_DIR"' EXIT

run_nexus workspace start "$ws_id" >/dev/null

DIST_DIR="$WORK_DIR/dist"
mkdir -p "$DIST_DIR"

echo "== export workspace artifact =="
set +e
export_out="$(cd "$REPO_DIR" && run_nexus workspace export "$ws_id" --out "$DIST_DIR/standalone-proof" 2>&1)"
export_status=$?
set -e
printf '%s\n' "$export_out"

if [[ $export_status -ne 0 ]]; then
  echo "e2e-standalone-lima: export not implemented or failed (expected until CLI-120+ lands)" >&2
  exit 2
fi

RUNNER="$DIST_DIR/standalone-proof"
BUNDLE="$DIST_DIR/standalone-proof.nxbundle"
if [[ ! -f "$RUNNER" ]] || [[ ! -f "$BUNDLE" ]]; then
  echo "e2e-standalone-lima: missing runner or bundle artifact" >&2
  exit 1
fi

echo "== copy artifacts to Lima guest =="
limactl shell "$LIMA_INSTANCE" -- sh -lc 'mkdir -p /tmp/nexus-standalone-proof'
cat "$RUNNER" | limactl shell "$LIMA_INSTANCE" -- sh -lc 'cat > /tmp/nexus-standalone-proof/runner && chmod +x /tmp/nexus-standalone-proof/runner'
cat "$BUNDLE" | limactl shell "$LIMA_INSTANCE" -- sh -lc 'cat > /tmp/nexus-standalone-proof/bundle.nxbundle'

echo "== standalone contract checks in Lima =="
limactl shell "$LIMA_INSTANCE" -- sh -lc '/tmp/nexus-standalone-proof/runner --help | tee /tmp/nexus-standalone-proof/help.txt >/dev/null'
limactl shell "$LIMA_INSTANCE" -- sh -lc 'grep -q "run" /tmp/nexus-standalone-proof/help.txt && grep -q "start" /tmp/nexus-standalone-proof/help.txt && grep -q "exec" /tmp/nexus-standalone-proof/help.txt && grep -q "stop" /tmp/nexus-standalone-proof/help.txt'

echo "== PASS: artifact exported and standalone contract verified in Lima =="
