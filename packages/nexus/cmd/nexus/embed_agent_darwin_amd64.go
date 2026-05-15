//go:build darwin && amd64

package main

import _ "embed"

// embeddedAgent is the statically linked Linux/amd64 guest agent embedded into the
// macOS/amd64 Nexus CLI (`nexus vm bake` injects into the guest ext4 prior to bake).
//
//go:generate sh -c "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o agent-linux-amd64 ../nexus-guest-agent/"
//go:embed agent-linux-amd64
var embeddedAgent []byte
