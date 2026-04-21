package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

// ptySessionInfo matches the pty.create response shape from the daemon.
type ptySessionInfo struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	WorkDir     string `json:"workDir"`
}

// ptyDataParams matches pty.data notification params.
type ptyDataParams struct {
	SessionID string `json:"sessionId"`
	Data      string `json:"data"`
}

// ptyExitParams matches pty.exit notification params.
type ptyExitParams struct {
	SessionID string `json:"sessionId"`
	ExitCode  int    `json:"exitCode"`
}

func runCommand() *cobra.Command {
	var workDir string

	cmd := &cobra.Command{
		Use:   "exec <workspace> -- <command> [args...]",
		Short: "Run a command in a workspace and stream its output",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			wsNameOrID := args[0]
			cmdArgs := args[1:]
			for i, a := range cmdArgs {
				if a == "--" {
					cmdArgs = cmdArgs[i+1:]
					break
				}
			}
			if len(cmdArgs) == 0 {
				return fmt.Errorf("a command is required after the workspace name")
			}

			conn, err := rpc.EnsureMux()
			if err != nil {
				return fmt.Errorf("nexus workspace exec: %w", err)
			}
			defer conn.Close()

			wsID, err := rpc.ResolveWorkspaceIDMux(cmd.Context(), conn, wsNameOrID)
			if err != nil {
				return err
			}

			cmdLine := shellJoin(cmdArgs)
			cols, rows := 80, 24

			dataCh, cancelData := conn.Subscribe("pty.data")
			defer cancelData()
			exitCh, cancelExit := conn.Subscribe("pty.exit")
			defer cancelExit()

			var session ptySessionInfo
			if err := conn.Call("pty.create", map[string]any{
				"workspaceId": wsID,
				"shell":       "/bin/sh",
				"args":        []string{"-c", cmdLine},
				"workDir":     workDir,
				"cols":        cols,
				"rows":        rows,
			}, &session); err != nil {
				return fmt.Errorf("nexus workspace exec: pty.create: %w", err)
			}

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt)
			defer signal.Stop(sigCh)
			go func() {
				if _, ok := <-sigCh; ok {
					_ = conn.Send("pty.write", map[string]any{
						"sessionId": session.ID,
						"data":      "\x03",
					})
				}
			}()

			for {
				select {
				case raw, ok := <-dataCh:
					if !ok {
						return nil
					}
					var p ptyDataParams
					if err := json.Unmarshal(raw, &p); err != nil {
						continue
					}
					if p.SessionID == session.ID {
						_, _ = os.Stdout.WriteString(p.Data)
					}
				case raw, ok := <-exitCh:
					if !ok {
						return nil
					}
					var p ptyExitParams
					if err := json.Unmarshal(raw, &p); err != nil {
						continue
					}
					if p.SessionID == session.ID {
						if p.ExitCode != 0 {
							os.Exit(p.ExitCode)
						}
						return nil
					}
				case <-cmd.Context().Done():
					_ = conn.Send("pty.close", map[string]any{"sessionId": session.ID})
					return context.Canceled
				}
			}
		},
	}
	cmd.Flags().StringVar(&workDir, "workdir", "/workspace", "working directory inside the workspace")
	cmd.Aliases = []string{"run"}
	return cmd
}

func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		escaped := strings.ReplaceAll(a, "'", `'\''`)
		parts[i] = "'" + escaped + "'"
	}
	return strings.Join(parts, " ")
}
