package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/spf13/cobra"
)

func shellCommand() *cobra.Command {
	var workDir string

	cmd := &cobra.Command{
		Use:   "shell <workspace>",
		Short: "Open an interactive shell in a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := rpc.EnsureMux()
			if err != nil {
				return fmt.Errorf("nexus workspace shell: %w", err)
			}
			defer conn.Close()

			wsID, err := rpc.ResolveWorkspaceIDMux(cmd.Context(), conn, args[0])
			if err != nil {
				return err
			}
			cols, rows := 80, 24
			stdinFd := int(os.Stdin.Fd())
			stdinIsCharDevice := false
			if stat, err := os.Stdin.Stat(); err == nil {
				stdinIsCharDevice = stat.Mode()&os.ModeCharDevice != 0
			}
			if term.IsTerminal(stdinFd) && stdinIsCharDevice {
				if w, h, e := term.GetSize(stdinFd); e == nil {
					cols, rows = w, h
				}
			}

			isTTY := term.IsTerminal(stdinFd) && stdinIsCharDevice
			if !isTTY {
				script, readErr := io.ReadAll(os.Stdin)
				if readErr != nil {
					return fmt.Errorf("nexus workspace shell: read stdin: %w", readErr)
				}
				if len(script) > 0 {
					return runShellScript(conn, cmd.Context(), wsID, workDir, cols, rows, script)
				}
			}

			dataCh, cancelData := conn.Subscribe("pty.data")
			defer cancelData()
			exitCh, cancelExit := conn.Subscribe("pty.exit")
			defer cancelExit()

			var session ptySessionInfo
			if err := conn.Call("pty.create", map[string]any{
				"workspaceId": wsID,
				"workDir":     workDir,
				"cols":        cols,
				"rows":        rows,
			}, &session); err != nil {
				return fmt.Errorf("nexus workspace shell: pty.create: %w", err)
			}

			var oldState *term.State
			if term.IsTerminal(stdinFd) {
				oldState, err = term.MakeRaw(stdinFd)
				if err != nil {
					return fmt.Errorf("nexus workspace shell: raw terminal: %w", err)
				}
				defer func() {
					if oldState != nil {
						_ = term.Restore(stdinFd, oldState)
					}
				}()
			}

			winchCh := make(chan os.Signal, 4)
			signal.Notify(winchCh, syscall.SIGWINCH)
			defer signal.Stop(winchCh)
			go func() {
				for range winchCh {
					if w, h, e := term.GetSize(stdinFd); e == nil {
						_ = conn.Send("pty.resize", map[string]any{
							"sessionId": session.ID,
							"cols":      w,
							"rows":      h,
						})
					}
				}
			}()

			go func() {
				buf := make([]byte, 512)
				for {
					n, err := os.Stdin.Read(buf)
					if n > 0 {
						_ = conn.Send("pty.write", map[string]any{
							"sessionId": session.ID,
							"data":      string(buf[:n]),
						})
					}
					if err != nil {
						return
					}
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
						if oldState != nil {
							_ = term.Restore(stdinFd, oldState)
							oldState = nil
						}
						if p.ExitCode != 0 {
							os.Exit(p.ExitCode)
						}
						return nil
					}
				case <-cmd.Context().Done():
					_ = conn.Send("pty.close", map[string]any{"sessionId": session.ID})
					return nil
				}
			}
		},
	}
	cmd.Flags().StringVar(&workDir, "workdir", "/workspace", "working directory inside the workspace")
	return cmd
}

func runShellScript(conn *rpc.MuxConn, ctx context.Context, wsID, workDir string, cols, rows int, script []byte) error {
	dataCh, cancelData := conn.Subscribe("pty.data")
	defer cancelData()
	exitCh, cancelExit := conn.Subscribe("pty.exit")
	defer cancelExit()

	var session ptySessionInfo
	if err := conn.Call("pty.create", map[string]any{
		"workspaceId": wsID,
		"shell":       "/bin/sh",
		"args":        []string{"-c", string(script)},
		"workDir":     workDir,
		"cols":        cols,
		"rows":        rows,
	}, &session); err != nil {
		return fmt.Errorf("nexus workspace shell: pty.create: %w", err)
	}

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
				if _, err := os.Stdout.WriteString(p.Data); err != nil {
					return err
				}
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
		case <-ctx.Done():
			_ = conn.Send("pty.close", map[string]any{"sessionId": session.ID})
			return context.Canceled
		}
	}
}
