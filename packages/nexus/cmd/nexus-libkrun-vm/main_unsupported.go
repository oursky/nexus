//go:build !linux || !cgo

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "nexus-libkrun-vm: requires Linux with CGO enabled")
	os.Exit(1)
}
