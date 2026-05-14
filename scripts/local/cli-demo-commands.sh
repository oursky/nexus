#!/usr/bin/env bash
# Inner script for asciinema: isolated daemon + workspace lifecycle (process backend).
set -euo pipefail

NEXUS="${NEXUS_BIN:?set NEXUS_BIN to the nexus binary}"
REPO="${DEMO_REPO:?set DEMO_REPO to an existing directory on the engine host}"
PORT="${NEXUS_DAEMON_PORT:?set NEXUS_DAEMON_PORT}"
TOKEN="${NEXUS_DAEMON_TOKEN:?set NEXUS_DAEMON_TOKEN}"

set -v
sleep 0.8

"$NEXUS" daemon start --port "$PORT" --token "$TOKEN"
sleep 2

"$NEXUS" daemon status
sleep 1.8

"$NEXUS" workspace list
sleep 1.5

created="$("$NEXUS" workspace create --repo "$REPO" --name demo --backend process)"
printf '%s\n' "$created"
sleep 2
WS_ID="$(printf '%s' "$created" | sed -n 's/.*(id: \(ws-[^)]*\)).*/\1/p')"
if [[ -z "$WS_ID" ]]; then
  echo "failed to parse workspace id from create output" >&2
  exit 1
fi

"$NEXUS" workspace start "$WS_ID"
sleep 3.5

"$NEXUS" workspace list
sleep 2

printf 'uname -a\npwd\nexit\n' | "$NEXUS" workspace shell demo
sleep 2

"$NEXUS" workspace stop "$WS_ID"
sleep 1.5

"$NEXUS" daemon stop --port "$PORT"
set +v
sleep 0.5
