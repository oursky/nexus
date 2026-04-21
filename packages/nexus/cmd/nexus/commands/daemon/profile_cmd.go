package daemon

import (
	"fmt"

	"github.com/oursky/nexus/packages/nexus/internal/infra/cli/profile"
	"github.com/spf13/cobra"
)

func profileCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "profile",
		Short: "Show current daemon profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := profile.LoadDefault()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "host:     %s\n", p.Host)
			fmt.Fprintf(cmd.OutOrStdout(), "port:     %d\n", p.Port)
			if p.SSHPort > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "sshPort:  %d\n", p.SSHPort)
			}
			if p.Token != "" {
				if len(p.Token) > 12 {
					fmt.Fprintf(cmd.OutOrStdout(), "token:    %s...%s\n", p.Token[:8], p.Token[len(p.Token)-4:])
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "token:    ***\n")
				}
			}
			return nil
		},
	}
}
