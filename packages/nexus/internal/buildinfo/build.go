// Package buildinfo holds version metadata injected at link time via -ldflags.
// All variables default to "dev" so unversioned local builds are identifiable.
package buildinfo

// These are set by scripts/remote/deploy.sh and scripts/local/deploy.sh via:
//
//	-ldflags "-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Time=..."
//	-ldflags "-X github.com/oursky/nexus/packages/nexus/internal/buildinfo.Commit=..."
var (
	// Time is the RFC-3339 UTC build timestamp (e.g. "2026-04-23T10:30:00Z").
	Time = "dev"

	// Commit is the short git commit hash at build time (e.g. "d52ff9e9").
	Commit = "dev"
)

// Info returns a human-readable one-line summary of the build.
func Info() string {
	return "nexus commit=" + Commit + " built=" + Time
}
