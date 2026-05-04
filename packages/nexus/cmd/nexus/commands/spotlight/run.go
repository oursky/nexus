package spotlight

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/oursky/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/sshtunnel"
	"github.com/spf13/cobra"
)

// runCommand is a long-running foreground command that:
//  1. Calls spotlight.start on the daemon for each discovered port
//  2. Starts a single SSH multi-tunnel
//  3. Prints "forwarded N/N ports" to stdout (Mac app scans for this line)
//  4. Blocks until SIGTERM/SIGINT or the SSH tunnel dies
//  5. Calls spotlight.stop on the daemon before exiting
//
// The Mac app launches this as a long-lived process, waits for the
// "forwarded" line in its stdout pipe, then stores the CLI PID.
// To stop: SIGTERM to this process.
func runCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "run <workspace-id>",
		Short:  "Run spotlight (long-lived; used by Mac app)",
		Args:   cobra.ExactArgs(1),
		Hidden: true, // internal; Mac app uses this, not end users
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceID := args[0]

			conn, err := rpc.EnsureMux()
			if err != nil {
				return fmt.Errorf("nexus spotlight run: %w", err)
			}
			defer conn.Close()

			// Discover ports.
			var ports []discoveredPort
			if err := conn.Call("workspace.discover-ports", map[string]any{"id": workspaceID}, &ports); err != nil {
				return fmt.Errorf("nexus spotlight run: port discovery failed: %w", err)
			}
			if len(ports) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no ports discovered")
				return nil
			}

			// Load profile for SSH tunneling.
			p, err := profile.LoadDefault()
			if err != nil {
				return fmt.Errorf("nexus spotlight run: %w", err)
			}

			// Create daemon-side spotlight forwards.
			type forwardResult struct {
				port discoveredPort
				fwd  *spotlight.Forward
			}
			var results []forwardResult
			for _, port := range ports {
				spec := spotlight.ExposeSpec{
					WorkspaceID: workspaceID,
					LocalPort:   port.LocalPort,
					RemotePort:  port.RemotePort,
					Protocol:    port.Protocol,
				}
				var result struct {
					Forward *spotlight.Forward `json:"forward"`
				}
				if err := conn.Call("spotlight.start", map[string]any{
					"workspaceId": workspaceID,
					"spec":        spec,
				}, &result); err != nil {
					_ = conn.Call("spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil)
					return fmt.Errorf("%d/%s: daemon forward failed: %w", port.LocalPort, port.Service, err)
				}
				results = append(results, forwardResult{port: port, fwd: result.Forward})
			}

			// Ensure cleanup: stop daemon-side forwards when we exit.
			// This runs regardless of how we exit (SIGTERM, tunnel death, etc.).
			defer func() {
				_ = conn.Call("spotlight.stop", map[string]any{"workspaceId": workspaceID}, nil)
			}()

			// Build SSH multi-tunnel.
			var fwds []sshtunnel.Forward
			for _, r := range results {
				rh := "127.0.0.1"
				rp := r.port.RemotePort
				if r.fwd != nil {
					if r.fwd.TargetHost != "" {
						rh = r.fwd.TargetHost
					}
					if r.fwd.RemotePort > 0 {
						rp = r.fwd.RemotePort
					}
				}
				fwds = append(fwds, sshtunnel.Forward{
					LocalPort:  r.port.LocalPort,
					RemoteHost: rh,
					RemotePort: rp,
				})
			}

			mt := sshtunnel.NewMultiWithOptions(p.Host, p.SSHPort, p.SSHIdentityFile, false)
			boundPorts, err := mt.Start(fwds, 5*time.Second)
			if err != nil {
				return fmt.Errorf("nexus spotlight run: SSH tunnel failed: %w", err)
			}
			defer mt.Close()

			// Print the "forwarded" line — Mac app waits for this before returning.
			for i, r := range results {
				label := r.port.Service
				if label == "" {
					label = fmt.Sprintf(":%d", r.port.RemotePort)
				}
				bp := boundPorts[i]
				if bp != r.port.LocalPort {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s → localhost:%d (remapped from :%d)\n", label, bp, r.port.LocalPort)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s → localhost:%d\n", label, bp)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "forwarded %d/%d ports\n", len(results), len(ports))

			// Block until SIGTERM/SIGINT or the SSH tunnel process exits.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

			tunnelDied := make(chan struct{})
			go func() {
				// Poll SSH tunnel liveness. mt.PID() returns 0 once Close() is called,
				// but that's our own cleanup — we only care about unexpected death here.
				pid := mt.PID()
				if pid <= 0 {
					return
				}
				proc, err := os.FindProcess(pid)
				if err != nil {
					close(tunnelDied)
					return
				}
				// Wait blocks until the process exits. On macOS/Linux this works fine.
				_, _ = proc.Wait()
				close(tunnelDied)
			}()

			select {
			case sig := <-sigCh:
				fmt.Fprintf(os.Stderr, "nexus spotlight run: received %s, shutting down\n", sig)
			case <-tunnelDied:
				fmt.Fprintf(os.Stderr, "nexus spotlight run: SSH tunnel exited unexpectedly\n")
			}

			// defer mt.Close() and defer spotlight.stop run here.
			return nil
		},
	}
	return cmd
}
