//go:build linux

package daemon

import (
	"os"
	"os/exec"
	"strings"
)

// pidsUsingUnixSocket lists PIDs holding the socket open via fuser(1).
// Used when lsof is missing or returns nothing so StopLocalDaemon can still signal the daemon.
func pidsUsingUnixSocket(path string) []int {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	out, err := exec.Command("fuser", path).CombinedOutput()
	if err != nil {
		return nil
	}
	// Typical: "/path/to/sock:             12345 67890\n"
	idx := strings.LastIndex(string(out), ":")
	if idx < 0 {
		return nil
	}
	fields := strings.Fields(string(out)[idx+1:])
	me := os.Getpid()
	var pids []int
	for _, f := range fields {
		pid := atoiLeadingDigits(f)
		if pid > 1 && pid != me {
			pids = append(pids, pid)
		}
	}
	return pids
}

func atoiLeadingDigits(s string) int {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	if n <= 1 {
		return 0
	}
	return n
}
