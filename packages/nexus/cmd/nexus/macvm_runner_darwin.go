//go:build darwin

package main

import (
	"github.com/oursky/nexus/packages/nexus/internal/infra/runtime/macvm"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:    "_macvm-runner",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			// RunVMSubprocess never returns — it either calls os.Exit or
			// krun_start_enter calls exit(). The return type is error to satisfy cobra.
			macvm.RunVMSubprocess(args[0])
			return nil
		},
	}
	extraCommands = append(extraCommands, cmd)
}
