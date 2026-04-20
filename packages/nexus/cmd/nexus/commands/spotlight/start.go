package spotlight

import (
	"fmt"
	"sync"

	"github.com/inizio/nexus/packages/nexus/cmd/nexus/commands/rpc"
	"github.com/inizio/nexus/packages/nexus/internal/domain/spotlight"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/inizio/nexus/packages/nexus/internal/infra/cli/sshtunnel"
	"github.com/spf13/cobra"
)

// tunnelCache holds active SSH port forwards keyed by remote port.
var tunnelCache struct {
	mu       sync.Mutex
	forwards map[int]*sshtunnel.Manager // localPort → tunnel
}

func startCommand() *cobra.Command {
	var workspaceID string
	var localPort int
	var remotePort int
	var protocol string

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a spotlight port forward",
		RunE: func(cmd *cobra.Command, args []string) error {
			if workspaceID == "" || localPort == 0 {
				return fmt.Errorf("nexus spotlight start: --workspace and --local-port are required")
			}
			if remotePort == 0 {
				remotePort = localPort
			}

			conn, err := rpc.EnsureDaemon()
			if err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}
			defer conn.Close()

			// Create daemon-side spotlight forward
			spec := spotlight.ExposeSpec{
				WorkspaceID: workspaceID,
				LocalPort:   localPort,
				RemotePort:  remotePort,
				Protocol:    protocol,
			}
			var result struct {
				Forward *spotlight.Forward `json:"forward"`
			}
			if err := rpc.Do(conn, "spotlight.start", map[string]any{
				"workspaceId": workspaceID,
				"spec":        spec,
			}, &result); err != nil {
				return fmt.Errorf("nexus spotlight start: %w", err)
			}

			forwardID := "unknown"
			if result.Forward != nil {
				forwardID = result.Forward.ID
			}

			// Auto-open SSH tunnel so port is accessible locally
			p, profErr := profile.LoadDefault()
			if profErr == nil {
				if tunnelErr := openSSHTunnel(p, localPort, remotePort); tunnelErr != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "spotlight started (%s) but SSH tunnel failed: %v\n", forwardID, tunnelErr)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "spotlight started (%s) → localhost:%d\n", forwardID, localPort)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "spotlight started (%s)\n", forwardID)
			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceID, "workspace", "", "workspace ID (required)")
	cmd.Flags().IntVar(&localPort, "local-port", 0, "local port to expose (required)")
	cmd.Flags().IntVar(&remotePort, "remote-port", 0, "remote port (defaults to local-port)")
	cmd.Flags().StringVar(&protocol, "protocol", "", "protocol (tcp/http)")
	return cmd
}

// openSSHTunnel creates an SSH tunnel: client localhost:localPort → daemon localhost:remotePort.
func openSSHTunnel(p *profile.Profile, localPort, remotePort int) error {
	tunnelCache.mu.Lock()
	defer tunnelCache.mu.Unlock()

	if tunnelCache.forwards == nil {
		tunnelCache.forwards = make(map[int]*sshtunnel.Manager)
	}

	if _, exists := tunnelCache.forwards[localPort]; exists {
		return nil // already tunnelling
	}

	tm := sshtunnel.New(p.Host, remotePort, p.SSHPort)
	if _, err := tm.EnsureWithLocalPort(localPort); err != nil {
		return err
	}

	tunnelCache.forwards[localPort] = tm
	return nil
}
