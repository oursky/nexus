//go:build (darwin || linux) && (amd64 || arm64)

package mutagenbin

import _ "embed"

//go:embed mutagen-agents.tar.gz
var embeddedMutagenAgents []byte
