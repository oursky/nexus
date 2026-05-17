//go:build linux

package start

import "github.com/oursky/nexus/packages/nexus/internal/infra/runtime/libkrun"

func readRootFSReleaseStamp(stampDir string) string {
	return libkrun.ReadRootFSReleaseStamp(stampDir)
}

func isRootFSReleaseStale(stampDir, expected string) bool {
	return libkrun.IsRootFSReleaseStale(stampDir, expected)
}
