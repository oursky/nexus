package libkrun

import (
	"context"
	"encoding/json"
	"errors"
	"net"
)

// AgentExecRequest is a command execution request to the guest agent.
// Matches the protocol used by the guest agent client.
type AgentExecRequest struct {
	ID      string   `json:"id"`
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	WorkDir string   `json:"workdir,omitempty"`
	Env     []string `json:"env,omitempty"`
	Stream  bool     `json:"stream,omitempty"`
}

// AgentExecResult holds the result of a guest command execution.
type AgentExecResult struct {
	ID       string `json:"id"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type agentEnvelope struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Stream   string `json:"stream,omitempty"`
	Data     string `json:"data,omitempty"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// agentExec executes a command on the guest agent over conn.
// conn is a Unix socket connection to the agent vsock port.
func agentExec(ctx context.Context, conn net.Conn, req AgentExecRequest) (AgentExecResult, error) {
	if conn == nil {
		return AgentExecResult{}, errors.New("agent conn: nil connection")
	}

	done := make(chan error, 1)
	go func() { done <- json.NewEncoder(conn).Encode(req) }()

	select {
	case <-ctx.Done():
		return AgentExecResult{}, ctx.Err()
	case err := <-done:
		if err != nil {
			return AgentExecResult{}, err
		}
	}

	stdout, stderr := "", ""
	respDone := make(chan error, 1)
	respOut := make(chan AgentExecResult, 1)

	go func() {
		dec := json.NewDecoder(conn)
		for {
			var env agentEnvelope
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
			respOut <- AgentExecResult{
				ID:       env.ID,
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
		return AgentExecResult{}, ctx.Err()
	case err := <-respDone:
		if err != nil {
			return AgentExecResult{}, err
		}
	}
	return <-respOut, nil
}
