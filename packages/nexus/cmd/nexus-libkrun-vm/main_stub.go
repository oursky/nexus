//go:build !libkrunvm

// Default builds (go test ./..., task build) skip the real libkrun CGO entrypoint.
// Rebuild the VM helper with: go build -tags libkrunvm (see scripts/local/build-libkrun-vm.sh).

package main

func main() {}
