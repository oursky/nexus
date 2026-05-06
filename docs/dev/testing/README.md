# Testing Guide (Nexus Core)

This guide defines the canonical test surfaces for `packages/nexus`.

## Test Surface Layout

- `packages/nexus/internal/**/**_test.go`
  - Unit and integration tests close to implementation.
  - Primary target for fast feedback and refactors.
- `packages/nexus/test/e2e/**/**_test.go`
  - Black-box daemon + CLI behavioral tests (`//go:build e2e`).
  - Primary target for cross-layer regressions and user-journey validation.
- `packages/nexus/test/e2e/harness/**`
  - Shared daemon/test harness utilities.

## Spec Traceability Contract

Tests that verify normative behavior must include a `Spec:` annotation directly above the `Test*` function:

```go
// Spec: VM-PROOF-004, VM-005
func TestCLI_WorkspaceForkAndRestore(t *testing.T) {
    ...
}
```

Rules:

- Use canonical IDs from `docs/spec/*` or `docs/dev/testing/formal-verification-matrix.md`
  exactly (e.g. `VM-PROOF-004`).
- Keep IDs narrowly scoped to what the test actually proves.
- If an obligation has no reliable automated test yet, add an explicit waiver in
  `packages/nexus/test/e2e/coverage/waivers.txt` with rationale.

## Commands

From repository root:

- `task test`
- `task check:spec-coverage`

From `packages/nexus`:

- `go test ./...`
- `go test -tags e2e -short ./test/e2e/...`
- `go run ./test/e2e/coverage --check`
