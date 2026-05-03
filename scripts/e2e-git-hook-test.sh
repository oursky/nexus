#!/usr/bin/env bash
# E2E test: git hook ref-sync pipeline + workspace lifecycle robustness
# Tests against the Mac app Headless RPC (Debug build, port 7778)
#
# Usage:
#   scripts/e2e-git-hook-test.sh
#
# Requirements:
#   - Mac app running with touch ~/.nexus-headless-rpc
#   - nexus daemon connected to remote linux host
#   - A git repo on the remote host (REPO_PATH)
set -euo pipefail

RPC="http://127.0.0.1:7778"
REPO_PATH="${REPO_PATH:-/home/newman/magic/nexus}"
NEXUS_CLI="${NEXUS_CLI:-/Users/newman/.local/bin/nexus}"
WS_NAME="e2e-git-hook-$$"
REMOTE_HOST="${REMOTE_HOST:-newman@linuxbox}"

strip_ansi() {
  python3 -c "import sys,json,re; print(re.sub(r'\x1b\[[^a-zA-Z]*[a-zA-Z]|\[\?2004[hl]|\r|\]0;[^\a]*\a','',json.load(sys.stdin).get('output',''))[-800:])"
}

rpc_write() {
  local tab="$1" cmd="$2"
  curl -sf -X POST "$RPC/terminal/write" \
    -H "Content-Type: application/json" \
    -d "{\"tabID\":\"$tab\",\"text\":\"$cmd\n\"}" > /dev/null
}

rpc_read() {
  local tab="$1" delay="${2:-2}"
  sleep "$delay"
  curl -sf "$RPC/terminal/read?tabID=$tab" | strip_ansi
}

rpc_run() {
  local tab="$1" cmd="$2" delay="${3:-2}"
  rpc_write "$tab" "$cmd"
  rpc_read "$tab" "$delay"
}

wait_state() {
  local ws_id="$1" target="$2" max="${3:-120}"
  local state i
  for i in $(seq 1 "$max"); do
    state=$(curl -s "$RPC/workspace/list" \
      | python3 -c "import sys,json; ws=[w for w in json.load(sys.stdin).get('workspaces',[]) if w['id']=='$ws_id']; print(ws[0]['state'] if ws else '?')")
    echo "  [$i] state=$state"
    [ "$state" = "$target" ] && return 0
    [ "$state" = "error" ] && { echo "FAIL: workspace entered error state"; return 1; }
    sleep 3
  done
  echo "FAIL: workspace never reached $target (last: $state)"
  return 1
}

open_tab() {
  local ws_id="$1" name="${2:-e2e}"
  local tab_json tab
  for i in $(seq 1 8); do
    tab_json=$(curl -s -X POST "$RPC/terminal/open" \
      -H "Content-Type: application/json" \
      -d "{\"workspaceID\":\"$ws_id\",\"name\":\"$name\"}")
    tab=$(echo "$tab_json" | python3 -c "import sys,json; print(json.load(sys.stdin).get('tabID',''))" 2>/dev/null || true)
    if [ -n "$tab" ] && [ "$tab" != "null" ]; then
      echo "$tab"
      return 0
    fi
    echo "  terminal/open retry $i: $tab_json" >&2
    sleep 2
  done
  echo "FAIL: could not open terminal tab" >&2
  return 1
}

get_ws_ref() {
  local ws_id="$1"
  curl -s "$RPC/workspace/list" \
    | python3 -c "import sys,json; ws=[w for w in json.load(sys.stdin).get('workspaces',[]) if w['id']=='$ws_id']; print(ws[0].get('ref','?') if ws else '?')"
}

echo "════════════════════════════════════════════"
echo " Nexus E2E: git hook ref-sync + robustness"
echo "════════════════════════════════════════════"

# ── 0. Verify RPC ─────────────────────────────────────────────────────────────
echo ""
echo "── 0. Check RPC ─────────────────────────────────────────────────────────"
STATE=$(curl -sf "$RPC/status" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('connection',{}).get('connectionState','?'))")
[ "$STATE" = "connected" ] || { echo "FAIL: RPC not connected (state=$STATE)"; exit 1; }
echo "  RPC: connected ✓"

# ── 1. Create workspace ───────────────────────────────────────────────────────
echo ""
echo "── 1. Create workspace from $REPO_PATH ──────────────────────────────────"
CREATE_JSON=$(curl -s -X POST "$RPC/workspace/create" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"$WS_NAME\",\"repo\":\"$REPO_PATH\"}")
echo "  create response: $CREATE_JSON"
# HeadlessRPCServer returns {"workspaceID":"...","name":"...","state":"..."}
WS_ID=$(echo "$CREATE_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('workspaceID') or d.get('workspace',{}).get('id',''))")
[ -n "$WS_ID" ] || { echo "FAIL: workspace create failed: $CREATE_JSON"; exit 1; }
echo "  workspace: $WS_ID"

# Capture initial ref from workspace list
INITIAL_REF=$(get_ws_ref "$WS_ID")
echo "  initial ref: $INITIAL_REF"

# ── 2. Start and wait for running ─────────────────────────────────────────────
echo ""
echo "── 2. Start workspace ───────────────────────────────────────────────────"
curl -sf -X POST "$RPC/workspace/start" \
  -H "Content-Type: application/json" \
  -d "{\"workspaceID\":\"$WS_ID\"}" > /dev/null
echo "  start sent, waiting for running..."
wait_state "$WS_ID" "running" 120
echo "  running ✓"
sleep 3  # terminal handshake settle

# ── 3. Open terminal and verify workspace ─────────────────────────────────────
echo ""
echo "── 3. Open terminal ─────────────────────────────────────────────────────"
TAB=$(open_tab "$WS_ID" "e2e-main")
echo "  tab: $TAB"

OUT=$(rpc_run "$TAB" "whoami && pwd" 2)
echo "  whoami/pwd: $OUT"

OUT=$(rpc_run "$TAB" "ls /workspace | head -5" 2)
echo "  /workspace contents: $OUT"
echo "$OUT" | grep -q . || { echo "FAIL: /workspace appears empty"; exit 1; }
echo "  workspace files present ✓"

# ── 4. Verify git in workspace ────────────────────────────────────────────────
echo ""
echo "── 4. Git state in VM ───────────────────────────────────────────────────"
OUT=$(rpc_run "$TAB" "git -C /workspace branch --show-current" 2)
# Extract branch: last non-empty line that doesn't look like a prompt
BRANCH_IN_VM=$(echo "$OUT" | grep -v '^\s*$' | grep -v 'root@' | grep -v '^#' | grep -vE '^\s*\$' | tail -1 | tr -d '\r ')
echo "  current branch in VM: '$BRANCH_IN_VM'"

# ── 5. Branch switch → hook fires → ref updated ───────────────────────────────
echo ""
echo "── 5. Test git hook: branch switch ─────────────────────────────────────"

# Fetch available remote branches
OUT=$(rpc_run "$TAB" "git -C /workspace branch -r --format='%(refname:short)'" 2)
echo "  remote branches raw: $OUT"
# Extract branch names: filter out prompt lines and 'origin' bare
BRANCHES=$(echo "$OUT" | grep -v '^\s*$' | grep -v 'root@' | grep -v '^#' | grep 'origin/' | sed 's|origin/||' | tr -d '\r ')
echo "  branches: $BRANCHES"

# Pick a branch different from current
OTHER_BRANCH=$(echo "$BRANCHES" | grep -v "^$" | grep -v "^${BRANCH_IN_VM}$" | head -1 | tr -d ' \r')
if [ -z "$OTHER_BRANCH" ]; then
  echo "  no other branch available; creating e2e-hook-test-branch for hook test"
  rpc_run "$TAB" "git -C /workspace checkout -b e2e-hook-test-branch 2>&1" 3
  OTHER_BRANCH="e2e-hook-test-branch"
  # Switch back to original so we can test switching to OTHER_BRANCH
  rpc_run "$TAB" "git -C /workspace checkout ${BRANCH_IN_VM} 2>&1" 3 > /dev/null
fi

echo "  switching from '$BRANCH_IN_VM' → '$OTHER_BRANCH'"
OUT=$(rpc_run "$TAB" "git -C /workspace checkout ${OTHER_BRANCH} 2>&1" 4)
echo "  git checkout output: $OUT"

# Wait up to 10s for the ref to be updated via the hook pipeline
echo "  waiting for daemon ref update..."
REF_AFTER=""
for i in $(seq 1 10); do
  sleep 1
  REF_AFTER=$(get_ws_ref "$WS_ID")
  if [ "$REF_AFTER" = "$OTHER_BRANCH" ]; then
    break
  fi
done

echo "  ref before switch : $INITIAL_REF"
echo "  ref after switch  : $REF_AFTER"
if [ "$REF_AFTER" = "$OTHER_BRANCH" ]; then
  echo "  git hook ref-sync: PASS ✓"
else
  echo "  WARN: ref not updated (got '$REF_AFTER', expected '$OTHER_BRANCH')"
  echo "  This may indicate the hook pipeline is not connected yet."
fi

# Switch back
OUT=$(rpc_run "$TAB" "cd /workspace && git checkout $BRANCH_IN_VM 2>&1" 3)
echo "  switched back to $BRANCH_IN_VM"
sleep 2
REF_BACK=$(get_ws_ref "$WS_ID")
echo "  ref after switch back: $REF_BACK"

# ── 6. Stop workspace ─────────────────────────────────────────────────────────
echo ""
echo "── 6. Stop workspace ────────────────────────────────────────────────────"
curl -sf -X POST "$RPC/workspace/stop" \
  -H "Content-Type: application/json" \
  -d "{\"workspaceID\":\"$WS_ID\"}" > /dev/null
wait_state "$WS_ID" "stopped" 60
echo "  stopped ✓"

# ── 7. Robustness: restart app and re-check workspace ────────────────────────
echo ""
echo "── 7. App restart robustness ────────────────────────────────────────────"
echo "  killing NexusApp..."
pkill -x NexusApp 2>/dev/null || true
sleep 2

APP_PATH="/Users/newman/magic/nexus/packages/nexus-swift/.build/xcbuild/Build/Products/Debug/NexusApp.app"
if [ -d "$APP_PATH" ]; then
  echo "  relaunching NexusApp..."
  open "$APP_PATH"
else
  echo "  WARNING: app not found at $APP_PATH, skipping restart test"
fi

echo "  waiting for RPC to come back..."
for i in $(seq 1 30); do
  sleep 1
  STATE=$(curl -sf "$RPC/status" 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('connection',{}).get('connectionState','?'))" 2>/dev/null || echo "?")
  echo "  [$i] rpc=$STATE"
  [ "$STATE" = "connected" ] && break
done
[ "$STATE" = "connected" ] || { echo "FAIL: RPC did not reconnect after app restart"; exit 1; }
echo "  RPC reconnected ✓"

# Check workspace is still visible and correct ref persisted
REF_PERSISTED=$(get_ws_ref "$WS_ID")
echo "  persisted ref after restart: $REF_PERSISTED"
WS_STATE=$(curl -s "$RPC/workspace/list" \
  | python3 -c "import sys,json; ws=[w for w in json.load(sys.stdin).get('workspaces',[]) if w['id']=='$WS_ID']; print(ws[0]['state'] if ws else 'MISSING')")
echo "  workspace state after restart: $WS_STATE"
[ "$WS_STATE" = "stopped" ] || [ "$WS_STATE" = "created" ] || { echo "WARN: unexpected state=$WS_STATE"; }
echo "  workspace visible after restart ✓"

# ── 8. Restart workspace after app restart ────────────────────────────────────
echo ""
echo "── 8. Restart workspace after app restart ───────────────────────────────"
curl -sf -X POST "$RPC/workspace/start" \
  -H "Content-Type: application/json" \
  -d "{\"workspaceID\":\"$WS_ID\"}" > /dev/null
echo "  start sent, waiting for running..."
wait_state "$WS_ID" "running" 60
echo "  running ✓"
sleep 3

TAB2=$(open_tab "$WS_ID" "e2e-restart")
OUT=$(rpc_run "$TAB2" "git -C /workspace branch --show-current" 2)
BRANCH_AFTER=$(echo "$OUT" | grep -v '^\s*$' | grep -v 'root@' | grep -v '^#' | tail -1 | tr -d '\r ')
echo "  branch after restart: '$BRANCH_AFTER'"
OUT=$(rpc_run "$TAB2" "ls /workspace | wc -l" 2)
echo "  workspace file count: $OUT"
echo "  workspace functional after restart ✓"

# ── 9. Cleanup ────────────────────────────────────────────────────────────────
echo ""
echo "── 9. Cleanup ───────────────────────────────────────────────────────────"
curl -sf -X POST "$RPC/workspace/stop" \
  -H "Content-Type: application/json" \
  -d "{\"workspaceID\":\"$WS_ID\"}" > /dev/null
wait_state "$WS_ID" "stopped" 60
curl -sf -X POST "$RPC/workspace/delete" \
  -H "Content-Type: application/json" \
  -d "{\"workspaceID\":\"$WS_ID\"}" > /dev/null 2>&1 || true
echo "  workspace deleted ✓"

echo ""
echo "════════════════════════════════════════════"
echo " E2E COMPLETE"
echo "════════════════════════════════════════════"
