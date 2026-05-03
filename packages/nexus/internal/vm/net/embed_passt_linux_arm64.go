//go:build linux && arm64

package net

import _ "embed"

//go:embed assets/passt-linux-arm64
var passtAssetData []byte
