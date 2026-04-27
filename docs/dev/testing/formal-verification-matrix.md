# Formal Verification Matrix

Source of truth: `docs/spec/09-vm-backend-formal-verification.md`.

Generated coverage map:

- `packages/nexus/test/e2e/coverage/coverage-map.md`

Regenerate and validate:

```bash
cd packages/nexus
go run ./test/e2e/coverage --check
```

The generated map classifies each formal-proof ID as:

- `covered` — one or more tests include matching `Spec:` annotations.
- `waived` — currently unautomated, but explicitly tracked with reason.
- `missing` — uncovered and not waived (check fails).
