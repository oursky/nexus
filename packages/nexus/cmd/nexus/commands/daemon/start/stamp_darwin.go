//go:build darwin

package start

import "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/macvm"

func readRootFSReleaseStamp(stampDir string) string {
	return macvm.ReadRootFSReleaseStamp(stampDir)
}

func isRootFSReleaseStale(stampDir, expected string) bool {
	return macvm.IsRootFSReleaseStale(stampDir, expected)
}
