//go:build darwin

package macvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// guestExecRequest mirrors the linux guest agent exec JSON envelope.
type guestExecRequest struct {
	ID      string   `json:"id"`
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type guestEnvelope struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Data     string `json:"data,omitempty"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type guestExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// guestExec probes the guest agent (same framing as linux libkrun agentExec).
func guestExec(ctx context.Context, conn net.Conn, req guestExecRequest) (guestExecResult, error) {
	if conn == nil {
		return guestExecResult{}, errors.New("agent conn: nil connection")
	}

	done := make(chan error, 1)
	go func() { done <- json.NewEncoder(conn).Encode(req) }()

	select {
	case <-ctx.Done():
		return guestExecResult{}, ctx.Err()
	case err := <-done:
		if err != nil {
			return guestExecResult{}, err
		}
	}

	stdout, stderr := "", ""
	respDone := make(chan error, 1)
	respOut := make(chan guestExecResult, 1)

	go func() {
		dec := json.NewDecoder(conn)
		for {
			var env guestEnvelope
			if err := dec.Decode(&env); err != nil {
				respDone <- err
				return
			}
			if env.Type == "chunk" {
				if env.Stream == "stderr" {
					stderr += env.Data
				} else {
					stdout += env.Data
				}
				continue
			}
			if env.Type == "result" {
				if env.Stdout != "" {
					stdout = env.Stdout
				}
				if env.Stderr != "" {
					stderr = env.Stderr
				}
			}
			respOut <- guestExecResult{
				ExitCode: env.ExitCode,
				Stdout:   stdout,
				Stderr:   stderr,
			}
			respDone <- nil
			return
		}
	}()

	select {
	case <-ctx.Done():
		return guestExecResult{}, ctx.Err()
	case err := <-respDone:
		if err != nil {
			return guestExecResult{}, err
		}
	}
	return <-respOut, nil
}

// workspaceReady runs a bounded /bin/true round-trip matching libkrun readiness semantics.
func (m *Manager) workspaceReady(ctx context.Context, workspaceID string) (bool, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return false, errors.New("workspace ID is required")
	}
	if _, ok := m.readyAgents.Load(workspaceID); ok {
		return true, nil
	}

	start := time.Now()
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	log.Printf("macvm readiness: probe start workspace=%s timeout=8s", workspaceID)
	conn, err := m.AgentConn(probeCtx, workspaceID)
	if err != nil {
		log.Printf("macvm readiness: agent dial not-ready workspace=%s duration=%s err=%v",
			workspaceID, time.Since(start).Round(time.Millisecond), err)
		return false, nil
	}
	defer conn.Close()

	result, err := guestExec(probeCtx, conn, guestExecRequest{
		ID:      fmt.Sprintf("ready-%d", time.Now().UnixNano()),
		Command: "/bin/true",
	})
	if err != nil {
		log.Printf("macvm readiness: agent exec not-ready workspace=%s duration=%s err=%v",
			workspaceID, time.Since(start).Round(time.Millisecond), err)
		return false, nil
	}
	if result.ExitCode != 0 {
		log.Printf("macvm readiness: agent exec not-ready workspace=%s duration=%s exit=%d stderr=%q",
			workspaceID, time.Since(start).Round(time.Millisecond), result.ExitCode, strings.TrimSpace(result.Stderr))
		return false, nil
	}

	m.readyAgents.Store(workspaceID, struct{}{})
	log.Printf("macvm readiness: agent probe ready workspace=%s duration=%s", workspaceID, time.Since(start).Round(time.Millisecond))
	return true, nil
}
