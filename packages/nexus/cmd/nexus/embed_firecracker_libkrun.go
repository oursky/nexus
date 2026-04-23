//go:build libkrun

package main

// embeddedFirecracker is empty in libkrun builds — Firecracker is not needed
// and the binary is not bundled.  The bootstrap skips Firecracker installation
// when the driver flag is "libkrun".
var embeddedFirecracker []byte
