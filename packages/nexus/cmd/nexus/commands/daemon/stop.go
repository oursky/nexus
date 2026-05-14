package daemon

import (
	"fmt"
	"io"
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
			n := StopLocalDaemon(cmd.OutOrStdout(), cmd.ErrOrStderr(), socketPath, port)
			if n == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No local nexus daemon processes found")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Stopped %d daemon process(es)\n", n)
			}
			return nil
		},
	}

	defaultData := start.DefaultDataDir()
	cmd.Flags().StringVar(&socketPath, "socket", filepath.Join(defaultData, "nexusd.sock"), "Unix socket path")
	cmd.Flags().IntVar(&port, "port", 7777, "Network listener port")
	return cmd
}

// StopLocalDaemon terminates processes listening on socketPath or on TCP port.
// It runs driver-specific residue cleanup before and after. Returns number of PIDs signaled.
func StopLocalDaemon(stdout, stderr io.Writer, socketPath string, port int) int {
	cleanupDaemonResidue()

	pids := make(map[int]struct{})
	for _, pid := range append(
		pidsFromLsof("lsof", "-t", "--", socketPath),
		pidsFromLsof("lsof", "-ti", fmt.Sprintf("tcp:%d", port))...,
	) {
		pids[pid] = struct{}{}
	}

	if len(pids) == 0 {
		cleanupDaemonResidue()
		return 0
	}

	my := os.Getpid()
	delete(pids, my)
	for pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}

	if len(pids) == 0 {
		cleanupDaemonResidue()
		return 0
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
			cleanupDaemonResidue()
			return len(pids)
		}
		time.Sleep(150 * time.Millisecond)
	}

	for pid := range pids {
		if processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}

	cleanupDaemonResidue()

	return len(pids)
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
