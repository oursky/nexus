# Nexus Docs

Isolated VM workspaces with fast local iteration.

## Quick start

```bash
curl -fsSL https://raw.githubusercontent.com/inizio/nexus/main/install.sh | bash
cd /path/to/project
nexus init && nexus create && nexus list && nexus start <workspace-id>
```

`nexus create` prints the id used by `start`, `ssh`, `tunnel`, and `stop`.

## By goal


| Goal | Doc |
|------|-----|
| Install | [`docs/guides/installation.md`](guides/installation.md) |
| Operations (doctor, backends, paths) | [`docs/guides/operations.md`](guides/operations.md) |
| Remote daemon on Linux | [`docs/guides/operations.md#remote-daemon-nexus-daemon-start-on-linux`](guides/operations.md#remote-daemon-nexus-daemon-start-on-linux) |
| Validate remote profile flow (QA checklist) | [`docs/guides/remote-profile-validation.md`](guides/remote-profile-validation.md) |
| Release signing keys | [`docs/guides/release-signing.md`](guides/release-signing.md) |
| CLI | [`docs/reference/cli.md`](reference/cli.md) |
| `nexus daemon` flags | [`docs/reference/cli.md#nexus-daemon-start-flags`](reference/cli.md#nexus-daemon-start-flags) |
| JS/TS SDK | [`docs/reference/sdk.md`](reference/sdk.md) |
| Host auth bundle format (SDK / advanced) | [`docs/reference/host-auth-bundle.md`](reference/host-auth-bundle.md) |
| Project structure | [`docs/reference/project-structure.md`](reference/project-structure.md) |
| `.nexus` config | [`docs/reference/workspace-config.md`](reference/workspace-config.md) |
