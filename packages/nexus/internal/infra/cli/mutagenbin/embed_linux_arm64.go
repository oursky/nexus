//go:build linux && arm64

package mutagenbin

import _ "embed"

// embeddedMutagen is the official Mutagen CLI for linux/arm64. Refresh with: task mutagen:update
//
//go:embed mutagen-linux-arm64
var embeddedMutagen []byte
