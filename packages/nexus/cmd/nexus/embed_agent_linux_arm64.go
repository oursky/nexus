//go:build linux && arm64

package main

import _ "embed"

// embeddedAgent contains the statically compiled nexus-guest-agent binary
// for linux/arm64.
//
//go:generate sh -c "CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o agent-linux-arm64 ../nexus-guest-agent/"
//go:embed agent-linux-arm64
var embeddedAgent []byte
