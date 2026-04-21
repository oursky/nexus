//go:build !((darwin || linux) && (amd64 || arm64))

package mutagenbin

// embeddedMutagenAgents is empty on platforms without a bundled Mutagen agents bundle.
var embeddedMutagenAgents []byte
