---
type: master
feature_area: code-quality-hardening
date: 2026-04-27
status: active
child_prds:
  - 01-size-decomposition/PRD.md
  - 02-abstraction-boundaries/PRD.md
  - 03-comment-dead-code-hygiene/PRD.md
  - 04-package-topology-restructure/PRD.md
  - 05-verification-rollout/PRD.md
  - 06-test-doc-rewrite/PRD.md
---

# Code Quality Hardening Cooldown (Master)

## Overview

This PRD is now the coordinator for a cooldown pass focused on maintainability after image/volume stabilization. The scope is `packages/nexus` and is non-functional by default: no new product features, no RPC contract breakages, and no dependency additions unless explicitly approved.

Primary goals:

1. Bring oversized files back under line-of-code gates.
2. Remove abstraction leaks and enforce clean layer boundaries.
3. Delete redundant comments and dead code.
4. Replace flat, flag-heavy command layout with nested package structure by concept.
5. Finish with objective verification evidence (build, lint, type, tests, static analysis).

## Child PRD Map

| Child PRD | Focus | Output |
|---|---|---|
| `01-size-decomposition/PRD.md` | LOC reduction and file splitting | Oversized-file backlog burned down |
| `02-abstraction-boundaries/PRD.md` | Layering and interface seams | Fewer concrete leaks and cleaner dependencies |
| `03-comment-dead-code-hygiene/PRD.md` | Comment policy and code deletion | Lower noise and smaller surface area |
| `04-package-topology-restructure/PRD.md` | Nested package/folder structure | Easier concept discovery and ownership |
| `05-verification-rollout/PRD.md` | Sequencing, CI gates, completion proof | Safe execution and hard evidence |
| `06-test-doc-rewrite/PRD.md` | Unit/e2e suite reorganization + spec traceability | Formal verification proof map and cleaner test surface |

## Global Constraints

- Keep behavior stable unless a child PRD explicitly calls out a deliberate behavior correction.
- Prefer delete/extract/move over introducing new abstraction layers.
- Use existing tooling first (`scripts/check-file-sizes.sh`, `golangci-lint`, `go test`, `staticcheck`).
- Keep diffs small and reversible; land in sequenced slices.

## Quality Gates

- Follow current `scripts/check-file-sizes.sh` thresholds.
  - Default `.go`/`.ts`: 400 lines.
  - `transport`/`storage`/`adapters` and `types.*`: 500 lines.
  - `domain`/`entities`/`models`: 300 lines.
- Target max 800 lines for test files that remain monolithic during transition.
- No new layer violations (`domain` isolated, `app` via domain ports, `infra` as implementations).
- No stale comments that only restate identifiers.
- No unreachable/unused feature paths left without explicit deprecation notes.

## Program Phases

1. `P1` Size decomposition (`01-size-decomposition`).
2. `P2` Boundary hardening (`02-abstraction-boundaries`).
3. `P3` Hygiene deletion (`03-comment-dead-code-hygiene`).
4. `P4` Package topology migration (`04-package-topology-restructure`).
5. `P5` Full verification and rollout close (`05-verification-rollout`).

## Dependency Graph

```text
P1 -> P2 -> P4
P1 -> P3
P2 -> P3
P2,P3,P4 -> P5
```

## Completion Criteria

- All child PRDs are marked complete with evidence links in their own docs.
- Oversized-file backlog for in-scope files is cleared or explicitly waived with rationale.
- Layer boundary checks pass and no known abstraction leak remains open.
- Comment/dead-code cleanup changes are merged without behavior regressions.
- Verification checklist in `05-verification-rollout/PRD.md` is fully green.

## Steer Log

### 2026-04-27 — Split monolithic PRD into nested execution tracks

- **Trigger**: Cooldown scope expanded across LOC, abstraction boundaries, comments, dead code, and package topology.
- **From**: Single large PRD with mixed concerns and coarse sequencing.
- **To**: Master coordinator + child PRDs with bounded scope and explicit dependencies.
- **Rationale**: Smaller PRDs reduce planning ambiguity, improve ownership clarity, and allow parallel execution without losing verification rigor.
