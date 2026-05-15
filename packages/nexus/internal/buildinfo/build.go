// Package buildinfo holds version metadata injected at link time via -ldflags.
// All variables default to "dev" so unversioned local builds are identifiable.
package buildinfo

import "strings"

// These are set by scripts/remote/deploy.sh and scripts/local/deploy.sh via:
//
//	-ldflags "-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Time=..."
//	-ldflags "-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Commit=..."
//	-ldflags "-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Version=..."
//	-ldflags "-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.DeployDomain=..."
var (
	// Version is the release version (e.g. "v1.2.3" or "dev").
	Version = "dev"

	// Time is the RFC-3339 UTC build timestamp (e.g. "2026-04-23T10:30:00Z").
	Time = "dev"

	// Commit is the short git commit hash at build time (e.g. "d52ff9e9").
	Commit = "dev"

	// DeployDomain is an optional prod engine hostname baked in at link time
	// (e.g. engine.example.com) — surfaced by `nexus daemon version` for eyeball checks.
	DeployDomain = ""
)

// Info returns a human-readable one-line summary of the build.
func Info() string {
	s := "nexus " + Version + " commit=" + Commit + " built=" + Time
	if d := strings.TrimSpace(DeployDomain); d != "" {
		s += " domain=" + d
	}
	return s
}
