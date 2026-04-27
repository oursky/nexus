//go:build !(linux && libkrun)

package main

// embeddedPasst is empty for non-libkrun builds.
var embeddedPasst []byte
