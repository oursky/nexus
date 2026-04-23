//go:build linux

package main

import (
	"io"
)

var initRuntimeBootstrapRunner func(projectRoot, runtimeName string) error = runInitRuntimeBootstrapLinux

func runInitRuntimeBootstrapLinux(projectRoot, runtimeName string) error {
	switch runtimeName {
	case "firecracker", "libkrun":
		return RunRootlessBootstrap(io.Discard, false, runtimeName)
	default:
		return nil
	}
}
