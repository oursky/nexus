// pty-host is a thin launcher for the PTY host RPC server. The implementation
// lives in package internal/ptyhost and is also embedded in the main nexus binary
// as "nexus __pty-host".
package main

import (
	"github.com/oursky/nexus/packages/nexus/internal/ptyhost"
)

func main() {
	ptyhost.Main()
}
