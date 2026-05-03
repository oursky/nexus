# Nexus System Specification — Chapter 10: macOS App Formal Verification

> **Status**: Normative

---

## Scope

This chapter defines formal verification requirements for the macOS app (`packages/nexus-swift`) so
desktop behavior remains safe and deterministic when daemon/runtime capabilities vary.

---

## App Invariants — `MACAPP-001`–`MACAPP-004`

**`MACAPP-001`** — Daemon compatibility status shown by the app MUST be derived from `/version`
protocol compatibility (`DaemonInfo.requiredProtocol`) with explicit dev-build exception
(`0.0.0-dev` is treated as compatible).

**`MACAPP-002`** — Outdated daemon protocol versions MUST surface as a distinct app state, not folded
into generic offline/disconnected states.

**`MACAPP-003`** — Remote auto-provisioning behavior from the app MUST be explicitly gated. When
provisioning is disabled, the app MUST fail with actionable guidance rather than attempting partial
host mutation.

**`MACAPP-004`** — Packaged app resources that include helper CLIs MUST preserve executable
permissions and app signing integrity requirements required for macOS launch.

---

## Formal Verification Obligations — `MACAPP-PROOF-001`–`MACAPP-PROOF-003`

**`MACAPP-PROOF-001 (Unit)`** — Unit tests MUST verify daemon compatibility classification logic,
including protocol threshold and dev-build compatibility exception.

**`MACAPP-PROOF-002 (Unit)`** — Unit tests MUST verify disabled provisioning returns the dedicated
error (`provisioningDisabled`) and does not proceed with upload/start flows.

**`MACAPP-PROOF-003 (Packaging)`** — Build/release verification MUST assert helper binaries are
re-signed with app entitlements and are executable in app bundle resources.

---

## Evidence Sources

- Swift tests under `packages/nexus-swift/Tests/NexusAppTests/`
- Packaging/signing steps in `packages/nexus-swift/project.yml`
