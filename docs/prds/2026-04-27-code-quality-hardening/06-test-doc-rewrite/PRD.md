---
type: child
parent_prd: ../PRD.md
feature_area: code-quality-hardening/test-doc-rewrite
date: 2026-04-28
status: draft
---

# Child PRD 06: Test and Documentation Rewrite

## Overview

This child PRD restructures the unit/integration/e2e testing surfaces and adds explicit linkage from tests to the formal verification spec (`docs/spec/09-vm-backend-formal-verification.md`).

## Goals

1. Make test-suite layout discoverable and consistent.
2. Add machine-readable spec references directly in tests.
3. Add a coverage generator that produces a formal verification matrix.
4. Gate PRs on unresolved formal-proof obligations unless explicitly waived.

## Scope

- `packages/nexus/internal/**/*_test.go` (unit + integration)
- `packages/nexus/test/e2e/**/*_test.go` (e2e)
- `packages/nexus/test/e2e/coverage/*` (coverage generator + waivers)
- `docs/dev/testing/*` (developer-facing testing docs)
- `Taskfile.yml` (spec-coverage check command)

## Conventions

Test functions use explicit spec tags in comments directly above the test:

```go
// Spec: VM-PROOF-004, VM-005
func TestCLI_WorkspaceForkAndRestore(t *testing.T) { ... }
```

## Task Graph

| ID | Task | Depends On | Output |
|---|---|---|---|
| TD-01 | Define test-suite structure docs and ownership boundaries | — | `docs/dev/testing/README.md` |
| TD-02 | Add formal spec tags to critical VM/e2e tests | TD-01 | tagged tests in e2e suites |
| TD-03 | Implement spec-coverage generator + waiver support | TD-02 | `coverage-map.md` generated |
| TD-04 | Add CI/local task entrypoint for spec coverage checks | TD-03 | `task check:spec-coverage` |
| TD-05 | Publish formal verification matrix docs | TD-03 | `docs/dev/testing/formal-verification-matrix.md` |

## Acceptance Criteria

- Unit/integration/e2e boundaries are documented and clear.
- VM formal verification IDs are traceable to tests or explicit waivers.
- Coverage generator fails on unknown IDs and unwaived missing proof obligations.
- Task-based command exists for repeatable local/CI verification.

## Known Limitations

- Some proof obligations need environment-heavy scenarios (daemon restart, full tool bootstrap) and may remain waived until dedicated harness support lands.
