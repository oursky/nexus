//go:build !windows

package runner

import "syscall"

// raiseRlimitNofile raises RLIMIT_NOFILE to the system hard limit.
// libkrun opens many file descriptors and requires a high fd limit.
// This is best-effort; errors are silently ignored.
func raiseRlimitNofile() error {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return err
	}
	lim.Cur = lim.Max
	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
}
