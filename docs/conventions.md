# Project Conventions

Last updated: 2026-05-17

## Go Code Generation

- **New packages with multiple files**: Write directly by the orchestrator. Background task agents are unreliable for file creation (hallucinate success without producing files).
- **Existing file modifications**: Background task agents can handle targeted edits when the existing content is embedded in the prompt as a verification anchor.
- See: retro-install-in-binary-2026-05-17.md

## Cross-Platform Patterns

- **Build tags over GOOS branching**: Prefer `_linux.go` / `_darwin.go` / `_unsupported.go` files with explicit build tags over `if runtime.GOOS` checks. Cleaner, vet-able, cross-compile-friendly.
- **Path resolution**: Never share path constants between macOS and Linux. Each platform resolves paths differently (e.g. macOS uses cache dir, Linux uses XDG share dir).
- **Cross-compile check**: `GOOS=darwin GOARCH=arm64 go vet ./...` from Linux before pushing cross-platform code.
- See: retro-install-in-binary-2026-05-17.md

## Stamp / State Files

- **Always `strings.TrimSpace()`** when reading file-based state. Assume external tooling may write trailing newlines.
- **Always provide fallback values**: When a stamp can be auto-derived from buildinfo, do so — don't silently skip.
- See: retro-install-in-binary-2026-05-17.md

## Anti-Patterns

- ❌ **Trusting background_task agents for file creation** — agents hallucinate success; verify with `ls`/`glob` immediately
- ❌ **Single-platform path assumptions** — use platform-specific files, not shared defaults
- ❌ **Silent stamp failure** — when version not explicitly provided, derive from buildinfo instead of skipping
- See: retro-install-in-binary-2026-05-17.md
