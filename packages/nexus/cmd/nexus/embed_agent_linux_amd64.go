//go:build linux && amd64

package main

import _ "embed"

// embeddedAgent contains the statically compiled nexus-guest-agent binary
// for linux/amd64. Embedded at build time; injected into rootfs on daemon start.
//
// To refresh:
//
//	go generate ./cmd/nexus
//
//go:generate sh -c "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o agent-linux-amd64 ../nexus-guest-agent/"
//go:embed agent-linux-amd64
var embeddedAgent []byte
