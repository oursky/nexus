package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	rpce "github.com/oursky/nexus/packages/nexus/internal/rpc/errors"
)

// ── SSH Check ────────────────────────────────────────────────────────────────

type sshCheckReq struct {
	ID string `json:"id"`
}

// SSHCheckResult is the response for workspace.sshcheck.
type SSHCheckResult struct {
	OK      bool   `json:"ok"`
	GuestIP string `json:"guestIp,omitempty"`
	Whoami  string `json:"whoami,omitempty"`
	Error   string `json:"error,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
}

// handleSSHCheck runs `ssh root@<host> [-p port] whoami` from the daemon host
// and returns whether the connection succeeded, along with any error details.
// This lets the Mac app verify SSH connectivity via RPC before opening Cursor.
//
// GuestIP may be a bare host (bridge networking) or host:port (libkrun port-
// forward). Both forms are handled: host:port is split into -p PORT root@host.
func (h *Handler) handleSSHCheck(ctx context.Context, raw json.RawMessage) (any, error) {
	req, err := decode[sshCheckReq](raw)
	if err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, rpce.InvalidParams("workspace.invalid_params", "id is required")
	}

	ws, err := h.svc.Info(ctx, req.ID)
	if err != nil {
		return nil, mapErr(err)
	}

	guestIP := ws.GuestIP
	if guestIP == "" {
		return &SSHCheckResult{
			OK:    false,
			Error: fmt.Sprintf("workspace %q (state: %s) has no guest IP — is it running?", req.ID, ws.State),
		}, nil
	}

	// Run ssh with a 15 s timeout from the daemon host.
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// GuestIP may be "host" or "host:port". Split accordingly.
	sshHost := guestIP
	sshPort := ""
	if h, p, splitErr := net.SplitHostPort(guestIP); splitErr == nil {
		sshHost = h
		sshPort = p
	}

	sshArgs := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-o", "LogLevel=ERROR",
	}
	if sshPort != "" {
		sshArgs = append(sshArgs, "-p", sshPort)
	}
	sshArgs = append(sshArgs, "root@"+sshHost, "whoami")

	cmd := exec.CommandContext(checkCtx, "ssh", sshArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	whoami := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())

	if runErr != nil {
		return &SSHCheckResult{
			OK:      false,
			GuestIP: guestIP,
			Error:   runErr.Error(),
			Stderr:  stderrStr,
		}, nil
	}

	return &SSHCheckResult{
		OK:      true,
		GuestIP: guestIP,
		Whoami:  whoami,
	}, nil
}
