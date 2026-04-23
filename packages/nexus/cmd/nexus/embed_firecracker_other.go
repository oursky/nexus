//go:build (!linux || (!amd64 && !arm64)) && !libkrun

package main

// embeddedFirecracker is empty on non-linux or unsupported arch builds.
// The daemon start setup path checks len(embeddedFirecracker) > 0 before
// attempting to install the binary.
var embeddedFirecracker []byte
