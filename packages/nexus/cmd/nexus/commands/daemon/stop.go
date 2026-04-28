package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oursky/nexus/packages/nexus/cmd/nexus/commands/daemon/start"
	"github.com/spf13/cobra"
)

func stopCommand() *cobra.Command {
	var (
		socketPath string
		port       int
	)

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running Nexus daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cleanupDaemonResidue()

			pids := make(map[int]struct{})
			for _, pid := range append(
				pidsFromLsof("lsof", "-t", "--", socketPath),
				pidsFromLsof("lsof", "-ti", fmt.Sprintf("tcp:%d", port))...,
			) {
				pids[pid] = struct{}{}
			}

			if len(pids) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No local nexus daemon processes found")
				return nil
			}

			for pid := range pids {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}

			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				allExited := true
				for pid := range pids {
					if processAlive(pid) {
						allExited = false
						break
					}
				}
				if allExited {
					fmt.Fprintf(cmd.OutOrStdout(), "Stopped %d daemon process(es)\n", len(pids))
					return nil
				}
				time.Sleep(150 * time.Millisecond)
			}

			for pid := range pids {
				if processAlive(pid) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Stopped %d daemon process(es)\n", len(pids))
			cleanupDaemonResidue()
			return nil
		},
	}

	defaultData := start.DefaultDataDir()
	cmd.Flags().StringVar(&socketPath, "socket", filepath.Join(defaultData, "nexusd.sock"), "Unix socket path")
	cmd.Flags().IntVar(&port, "port", 7777, "Network listener port")
	return cmd
}

func pidsFromLsof(name string, args ...string) []int {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(string(out), "\n")
	var pids []int
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if pid, err := strconv.Atoi(line); err == nil && pid > 0 && pid != os.Getpid() {
			pids = append(pids, pid)
		}
	}
	return pids
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
