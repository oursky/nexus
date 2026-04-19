package daemon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/inizio/nexus/packages/nexus/internal/auth/tokenstore"
	"github.com/inizio/nexus/packages/nexus/internal/profile"
	"github.com/inizio/nexus/packages/nexus/internal/tunnel"
	"github.com/spf13/cobra"
)

func connectCommand() *cobra.Command {
	var (
		name       string
		remotePort int
		sshPort    int
		identity   string
		token      string
		setDefault bool
	)

	cmd := &cobra.Command{
		Use:   "connect <ssh-target>",
		Short: "Connect to a remote Nexus daemon via SSH tunnel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sshTarget := args[0]

			if token == "" {
				token = os.Getenv("NEXUS_DAEMON_TOKEN")
			}
			if token == "" {
				var err error
				token, err = tokenstore.LoadOrGenerate()
				if err != nil {
					return fmt.Errorf("connect: token: %w", err)
				}
			}

			profileName := name
			if profileName == "" {
				profileName = sshHostname(sshTarget)
			}

			p := profile.Profile{
				ID:          uuid.New().String(),
				Name:        profileName,
				RemoteHost:  sshTarget,
				RemotePort:  remotePort,
				SSHPort:     sshPort,
				SSHIdentity: identity,
				LocalPort:   0,
				Token:       token,
				IsDefault:   setDefault,
				CreatedAt:   time.Now().UTC().Format(time.RFC3339),
			}

			store := profile.NewStore()
			if err := store.AddOrUpdate(p); err != nil {
				return fmt.Errorf("connect: save profile: %w", err)
			}

			mgr := tunnel.New(tunnel.TunnelConfig{
				SSHTarget:   sshTarget,
				SSHPort:     sshPort,
				SSHIdentity: identity,
				RemotePort:  remotePort,
				LocalPort:   0,
			})

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			if err := mgr.Start(ctx); err != nil {
				return fmt.Errorf("connect: tunnel start: %w", err)
			}

			localPort := mgr.LocalPort()
			fmt.Fprintf(cmd.OutOrStdout(), "nexus: tunnel up → ws://127.0.0.1:%d (profile: %s)\n", localPort, profileName)

			p.LocalPort = localPort
			if err := store.AddOrUpdate(p); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "nexus: warning: could not update profile port: %v\n", err)
			}

			if setDefault {
				if err := store.SetDefault(p.ID); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "nexus: warning: could not set default: %v\n", err)
				}
			}

			<-ctx.Done()
			mgr.Stop()
			fmt.Fprintln(cmd.OutOrStdout(), "nexus: tunnel closed")
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Profile display name (default: hostname from ssh-target)")
	cmd.Flags().IntVar(&remotePort, "remote-port", 7777, "Daemon port on remote host")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&identity, "identity", "", "Path to SSH identity file")
	cmd.Flags().StringVar(&token, "token", "", "Bearer token (or set NEXUS_DAEMON_TOKEN env var)")
	cmd.Flags().BoolVar(&setDefault, "set-default", false, "Set this profile as default after connecting")

	return cmd
}

func sshHostname(target string) string {
	if at := strings.LastIndex(target, "@"); at >= 0 {
		return target[at+1:]
	}
	return target
}
