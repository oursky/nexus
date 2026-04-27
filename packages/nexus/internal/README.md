# Internal Test Organization

Unit and integration tests for internal packages live next to implementation files.

Guidelines:

- Keep tests close to the package under test.
- Prefer table-driven unit tests for pure domain and adapter logic.
- Use integration tests for storage/service interactions that require real persistence or process boundaries.
- Add `Spec:` annotations on tests that assert normative behavior from `docs/spec/*`.
