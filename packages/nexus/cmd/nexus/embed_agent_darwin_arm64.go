//go:build darwin && arm64

package main

import _ "embed"

// embeddedAgent is the statically linked Linux/arm64 guest agent embedded into the
// macOS/arm64 Nexus CLI so `nexus vm bake` can inject updates into rootfs builds.
//
//go:generate sh -c "CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o agent-linux-arm64 ../nexus-guest-agent/"
//go:embed agent-linux-arm64
var embeddedAgent []byte
