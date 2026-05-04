//go:build linux

package libkrun

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitHookVSockPort is the vsock port the guest connects to when a git hook fires.
// The host listens on a Unix socket at vsock_10791.sock (listen=false); the
// guest dials vsock CID 2 (host), port 10791, and sends a JSON payload.
const GitHookVSockPort uint32 = 10791

// gitHookSocketPath is the Unix socket path inside the guest VM that the
// post-checkout hook writes to. The guest agent forwards from here to vsock.
const gitHookSocketPath = "/tmp/nexus-git-hook.sock"

// gitHookMsg is the JSON payload sent by the guest on branch change.
type gitHookMsg struct {
	WorkspaceID string `json:"workspaceID"`
	Ref         string `json:"ref"`
}

// GitHookCallback is called by the proxy when a branch-change notification
// arrives from inside a workspace VM.
type GitHookCallback func(workspaceID, ref string)

// gitHookSockPath returns the host-side Unix socket path for the git hook proxy
// of a given workspace (the path libkrun maps to guest vsock port 10791).
func gitHookSockPath(workDir string) string {
	return filepath.Join(workDir, fmt.Sprintf("vsock_%d.sock", GitHookVSockPort))
}

// startGitHookProxy starts a Unix socket listener that receives branch-change
// notifications from the guest via libkrun vsock mapping.
//
// libkrun maps guest vsock (CID 2, port GitHookVSockPort) connections to the
// Unix socket at sockPath (listen=false: guest is the dialer, host is the server).
func startGitHookProxy(sockPath string, cb GitHookCallback) {
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Printf("[libkrun] git hook proxy: listen on %s: %v", sockPath, err)
		return
	}

	log.Printf("[libkrun] git hook proxy listening on %s", sockPath)

	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleGitHookConn(conn, cb)
		}
	}()
}

func handleGitHookConn(conn net.Conn, cb GitHookCallback) {
	defer conn.Close()

	var msg gitHookMsg
	if err := json.NewDecoder(conn).Decode(&msg); err != nil {
		log.Printf("[libkrun] git hook proxy: decode: %v", err)
		return
	}

	msg.WorkspaceID = strings.TrimSpace(msg.WorkspaceID)
	msg.Ref = strings.TrimSpace(msg.Ref)
	if msg.WorkspaceID == "" || msg.Ref == "" {
		log.Printf("[libkrun] git hook proxy: empty workspaceID or ref, ignoring")
		return
	}

	log.Printf("[libkrun] git hook: workspace %s → ref %s", msg.WorkspaceID, msg.Ref)
	if cb != nil {
		cb(msg.WorkspaceID, msg.Ref)
	}
}

// installGitHook writes a post-checkout hook into <projectRoot>/.git/hooks/.
// The hook runs inside the VM (on the virtiofs-mounted project directory) and
// sends a branch-change notification to the daemon via the guest agent unix socket.
func installGitHook(projectRoot, workspaceID string) {
	hooksDir, err := resolveGitHooksDir(projectRoot)
	if err != nil {
		log.Printf("[libkrun] git hook: skip install in %s: %v", projectRoot, err)
		return
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		log.Printf("[libkrun] git hook: mkdir %s: %v", hooksDir, err)
		return
	}

	hookPath := filepath.Join(hooksDir, "post-checkout")
	// $3 == 1 means branch switch; $3 == 0 means file checkout — only notify on branch switch.
	// We use python3 to send the Unix socket message because nc/socat are not
	// guaranteed to be installed in the VM rootfs.
	//
	// The Python snippet is written into a temp file so we avoid shell quoting
	// issues when embedding double-quoted JSON inside a -c "..." argument.
	// The temp file path is deterministic so it's only written once per hook run.
	script := fmt.Sprintf(`#!/bin/sh
# Nexus post-checkout hook — notifies the daemon of branch changes.
# $3 == 1 means branch switch (not file checkout).
[ "$3" = "1" ] || exit 0
ref=$(git symbolic-ref --short HEAD 2>/dev/null) || exit 0
[ -n "$ref" ] || exit 0
_notify=/tmp/nexus-git-notify.py
if [ ! -f "$_notify" ]; then
cat > "$_notify" << 'PYEOF'
import socket, sys
ws = sys.argv[1]
ref = sys.argv[2]
sock = sys.argv[3]
msg = ('{"workspaceID":"' + ws + '","ref":"' + ref + '"}').encode()
try:
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(2)
    s.connect(sock)
    s.sendall(msg)
    s.close()
except Exception:
    pass
PYEOF
fi
python3 "$_notify" "%s" "$ref" "%s" 2>/dev/null || true
`, workspaceID, gitHookSocketPath)

	if err := os.WriteFile(hookPath, []byte(script), 0o755); err != nil {
		log.Printf("[libkrun] git hook: write %s: %v", hookPath, err)
	}
}

func resolveGitHooksDir(projectRoot string) (string, error) {
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--git-path", "hooks")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve hooks path: %w: %s", err, strings.TrimSpace(string(out)))
	}
	hooksDir := strings.TrimSpace(string(out))
	if hooksDir == "" {
		return "", fmt.Errorf("resolve hooks path: empty output")
	}
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(projectRoot, hooksDir)
	}
	return hooksDir, nil
}
