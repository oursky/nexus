# Spec Coverage Map

Source spec directory: `../../docs/spec`

| Spec ID | Status | Test References | Notes |
|---|---|---|---|
| AUTH-001 | waived | - | Conceptual clause: definition of auth relay tokens. |
| AUTH-002 | waived | - | Conceptual clause: token opacity. |
| AUTH-003 | waived | - | Conceptual clause: token instance validity. |
| AUTH-004 | waived | - | Conceptual clause: expired vs revoked tokens. |
| AUTH-005 | waived | - | Conceptual clause: distinction between relay token and daemon bearer token. |
| AUTH-010 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-011 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-012 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-013 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-014 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-015 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-016 | waived | - | Conceptual clause: auth relay token distinct from daemon bearer token (restated from AUTH-005). |
| AUTH-017 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-018 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| AUTH-019 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| CLI-001 | waived | - | General CLI connectivity behavior; covered by existing CLI tests implicitly. |
| CLI-002 | covered | `TestCLI_DaemonUnreachable` (test/e2e/cli/cli_test.go:151) |  |
| CLI-003 | covered | `TestCLI_UnknownSubcommand` (test/e2e/cli/cli_test.go:135) |  |
| CLI-004 | waived | - | General CLI output behavior; verified by all CLI tests. |
| CLI-005 | waived | - | General CLI --json behavior; not all commands support --json in e2e yet. |
| CLI-006 | waived | - | General CLI workspace argument resolution; verified by existing CLI tests. |
| CLI-010 | waived | - | nexus daemon start flags; requires testing daemon start directly. |
| CLI-011 | waived | - | nexus daemon start self-daemonize behavior. |
| CLI-012 | waived | - | nexus daemon start --foreground behavior. |
| CLI-013 | waived | - | nexus daemon start token resolution. |
| CLI-014 | waived | - | nexus daemon start backend selection. |
| CLI-015 | waived | - | nexus daemon start guest agent injection. |
| CLI-016 | waived | - | nexus daemon start auto-setup behavior. |
| CLI-017 | waived | - | nexus daemon start failure exit code. |
| CLI-018 | waived | - | nexus daemon start readiness signal. |
| CLI-019 | waived | - | nexus daemon start NEXUS_DAEMON_SERGE env var. |
| CLI-020 | waived | - | nexus daemon stop command. |
| CLI-030 | covered | `TestCLI_WorkspaceCreate_EndToEnd` (test/e2e/cli/workspace_create_test.go:15) |  |
| CLI-031 | covered | `TestCLI_WorkspaceCreate_EndToEnd` (test/e2e/cli/workspace_create_test.go:15) |  |
| CLI-032 | covered | `TestCLI_WorkspaceCreate_EndToEnd` (test/e2e/cli/workspace_create_test.go:15) |  |
| CLI-033 | covered | `TestCLI_WorkspaceCreate_EndToEnd` (test/e2e/cli/workspace_create_test.go:15) |  |
| CLI-034 | covered | `TestCLI_WorkspaceCreate_EndToEnd` (test/e2e/cli/workspace_create_test.go:15) |  |
| CLI-035 | covered | `TestCLI_WorkspaceCreate_EndToEnd` (test/e2e/cli/workspace_create_test.go:15) |  |
| CLI-036 | waived | - | nexus workspace list command. |
| CLI-037 | waived | - | nexus workspace info command. |
| CLI-038 | waived | - | nexus workspace info --json. |
| CLI-039 | waived | - | nexus workspace start command. |
| CLI-040 | waived | - | nexus workspace start post-start port discovery. |
| CLI-041 | waived | - | nexus workspace start port discovery behavior. |
| CLI-042 | waived | - | nexus workspace stop command. |
| CLI-043 | waived | - | nexus workspace remove command. |
| CLI-044 | covered | `TestCLI_WorkspaceForkAndRestore` (test/e2e/cli/fork_restore_test.go:14) |  |
| CLI-045 | covered | `TestCLI_WorkspaceForkAndRestore` (test/e2e/cli/fork_restore_test.go:14) |  |
| CLI-046 | covered | `TestCLI_WorkspaceForkAndRestore` (test/e2e/cli/fork_restore_test.go:14) |  |
| CLI-047 | covered | `TestCLI_WorkspaceForkAndRestore` (test/e2e/cli/fork_restore_test.go:14) |  |
| CLI-048 | covered | `TestCLI_WorkspaceForkAndRestore` (test/e2e/cli/fork_restore_test.go:14) |  |
| CLI-049 | covered | `TestCLI_WorkspaceShellPTY` (test/e2e/cli/interactive_test.go:20) |  |
| CLI-050 | covered | `TestCLI_WorkspaceShellPTY` (test/e2e/cli/interactive_test.go:20) |  |
| CLI-051 | covered | `TestCLI_WorkspaceShellPTY` (test/e2e/cli/interactive_test.go:20) |  |
| CLI-052 | waived | - | nexus workspace exec command details. |
| CLI-053 | waived | - | nexus workspace exec --workdir. |
| CLI-054 | waived | - | nexus workspace exec pty.create args. |
| CLI-055 | waived | - | nexus workspace exec exit code propagation. |
| CLI-056 | waived | - | nexus workspace run alias stopgap note. |
| CLI-057 | waived | - | nexus workspace ports list command. |
| CLI-060 | waived | - | nexus spotlight start command. |
| CLI-061 | waived | - | nexus spotlight start behavior details. |
| CLI-062 | waived | - | nexus spotlight start port remapping. |
| CLI-063 | waived | - | nexus spotlight start exit code on failure. |
| CLI-064 | waived | - | nexus spotlight list command. |
| CLI-065 | waived | - | nexus spotlight stop command. |
| CLI-066 | waived | - | nexus spotlight stop closes all forwards. |
| CLI-067 | waived | - | nexus spotlight add command. |
| CLI-068 | waived | - | nexus spotlight add flags. |
| CLI-069 | waived | - | nexus spotlight add workspace.ports.add call. |
| CLI-070 | waived | - | nexus spotlight remove list command. |
| CLI-071 | waived | - | nexus spotlight remove command. |
| CLI-072 | waived | - | nexus spotlight remove --force. |
| CLI-080 | covered | `TestCLI_ProjectCreateListGetRemove` (test/e2e/cli/cli_test.go:17) |  |
| CLI-081 | covered | `TestCLI_ProjectCreateListGetRemove` (test/e2e/cli/cli_test.go:17) |  |
| CLI-082 | covered | `TestCLI_ProjectCreateListGetRemove` (test/e2e/cli/cli_test.go:17) |  |
| CLI-083 | covered | `TestCLI_ProjectCreateListGetRemove` (test/e2e/cli/cli_test.go:17) |  |
| CLI-090 | waived | - | nexus dev up command. |
| CLI-091 | waived | - | nexus dev up behavior pipeline. |
| CLI-092 | covered | `TestWorkspaceIdempotency` (test/e2e/workspace/idempotency_test.go:15) |  |
| CLI-093 | waived | - | nexus dev up exit codes. |
| CLI-094 | waived | - | nexus dev up port readiness polling. |
| CLI-095 | waived | - | nexus dev down command. |
| CLI-096 | waived | - | nexus dev down behavior pipeline. |
| CLI-097 | waived | - | nexus dev down exit code when no session. |
| CLI-098 | waived | - | nexus dev status command. |
| CLI-100 | waived | - | nexus config validate command. |
| CLI-101 | waived | - | nexus config validate behavior pipeline. |
| CLI-102 | waived | - | nexus config migrate command. |
| CLI-103 | waived | - | nexus config migrate backup behavior. |
| CLI-110 | waived | - | Deploy stub; not yet implemented. |
| CLI-111 | waived | - | Deploy stub; not yet implemented. |
| DAEMON-001 | waived | - | Conceptual clause: daemon is a single long-running process. |
| DAEMON-002 | waived | - | Conceptual clause: CLI is a client process. |
| DAEMON-003 | waived | - | Conceptual clause: connectivity modes table. |
| DAEMON-004 | waived | - | Conceptual clause: data directory resolution order. |
| DAEMON-005 | waived | - | Conceptual clause: default socket and database paths. |
| DAEMON-006 | waived | - | Conceptual clause: runtime backend selection. |
| DAEMON-007 | waived | - | Conceptual clause: self-daemonization description. |
| DAEMON-008 | waived | - | Conceptual clause: guest agent injection description. |
| DAEMON-009 | waived | - | Conceptual clause: log file location. |
| DAEMON-010 | waived | - | Conceptual clause: network listener defaults. |
| DAEMON-020 | covered | `TestNodeInfo` (test/e2e/daemon/info_test.go:12)<br>`TestProtocol_NodeInfo` (test/e2e/workspace/protocol_test.go:62) |  |
| DAEMON-021 | covered | `TestNodeInfo` (test/e2e/daemon/info_test.go:12)<br>`TestProtocol_NodeInfo` (test/e2e/workspace/protocol_test.go:62) |  |
| DAEMON-022 | covered | `TestNodeInfo` (test/e2e/daemon/info_test.go:12)<br>`TestProtocol_NodeInfo` (test/e2e/workspace/protocol_test.go:62) |  |
| DAEMON-023 | covered | `TestNodeInfo` (test/e2e/daemon/info_test.go:12)<br>`TestProtocol_NodeInfo` (test/e2e/workspace/protocol_test.go:62) |  |
| DAEMON-024 | covered | `TestNodeInfo` (test/e2e/daemon/info_test.go:12)<br>`TestProtocol_NodeInfo` (test/e2e/workspace/protocol_test.go:62) |  |
| DAEMON-025 | covered | `TestNodeInfo` (test/e2e/daemon/info_test.go:12)<br>`TestProtocol_NodeInfo` (test/e2e/workspace/protocol_test.go:62) |  |
| DAEMON-026 | waived | - | Internal implementation: daemon.log.tail log file reading. |
| DAEMON-027 | waived | - | Internal implementation: daemon.log.tail response shape. |
| DAEMON-028 | waived | - | Internal implementation: daemon.log.tail truncation behavior. |
| DAEMON-030 | waived | - | Internal implementation: data directory resolution from environment. |
| DAEMON-031 | waived | - | Internal implementation: default socket path derivation. |
| DAEMON-032 | waived | - | Internal implementation: default database path derivation. |
| DAEMON-040 | waived | - | Internal implementation: host prerequisites setup step. |
| DAEMON-041 | waived | - | Internal implementation: SQLite database open/creation. |
| DAEMON-042 | waived | - | Internal implementation: stale socket removal on startup. |
| DAEMON-043 | waived | - | Internal implementation: RPC handler registration order. |
| DAEMON-044 | waived | - | Internal implementation: socket readiness signal timing. |
| DAEMON-045 | waived | - | Internal implementation: node.info first-call guarantee. |
| DAEMON-046 | waived | - | Internal implementation: guest agent injection caching. |
| DAEMON-047 | waived | - | Internal implementation: libkrun missing asset exit code. |
| DAEMON-048 | waived | - | Internal implementation: HTTP server startup timing. |
| DAEMON-049 | waived | - | Internal implementation: self-daemonize re-exec details. |
| DAEMON-050 | waived | - | Internal implementation: NEXUS_DAEMON_FOREGROUND behavior. |
| DAEMON-051 | waived | - | Internal implementation: bearer token resolution order. |
| DAEMON-052 | waived | - | Internal implementation: TLS requirement on non-loopback bind. |
| DAEMON-053 | waived | - | Internal implementation: token value must not be logged. |
| DAEMON-054 | covered | `TestProtocol_AuthReject` (test/e2e/workspace/protocol_test.go:87) |  |
| DAEMON-055 | waived | - | Internal implementation: Unix socket filesystem permissions. |
| DAEMON-058 | waived | - | Internal implementation: TLS mode enforcement. |
| DAEMON-060 | waived | - | Internal implementation: SIGTERM/SIGINT graceful shutdown trigger. |
| DAEMON-061 | waived | - | Internal implementation: shutdown closes spotlight listeners. |
| DAEMON-062 | waived | - | Internal implementation: shutdown closes PTY sessions. |
| DAEMON-063 | waived | - | Internal implementation: shutdown closes SQLite database. |
| DAEMON-064 | waived | - | Internal implementation: shutdown removes Unix socket file. |
| DAEMON-065 | waived | - | Internal implementation: graceful shutdown timeout. |
| DAEMON-066 | waived | - | Internal implementation: clean shutdown exit code. |
| DAEMON-067 | waived | - | Internal implementation: SIGKILL non-graceful behavior. |
| ERR-001 | waived | - | General rule verified by all RPC error tests returning -32000. |
| ERR-002 | waived | - | General rule verified structurally by all error response handling in the RPC layer. |
| ERR-003 | waived | - | General rule: message field human-readability is a design constraint, not a single testable behavior. |
| ERR-004 | covered | `TestErrors_MissingIDParam` (test/e2e/workspace/errors_test.go:70)<br>`TestErrors_SpotlightStopMissingWorkspaceID` (test/e2e/workspace/errors_test.go:96)<br>`TestErrors_MethodNotFound` (test/e2e/workspace/errors_test.go:119) |  |
| ERR-005 | waived | - | General rule: all application errors use -32000 with data.kind — verified structurally. |
| ERR-010 | covered | `TestErrors_DuplicateWorkspaceName` (test/e2e/workspace/errors_test.go:146) |  |
| ERR-011 | covered | `TestErrors_WorkspaceNotFound` (test/e2e/workspace/errors_test.go:13)<br>`TestErrors_PTYCreateMissingWorkspace` (test/e2e/workspace/errors_test.go:130)<br>`TestLifecycle_NotFound` (test/e2e/workspace/lifecycle_sm_test.go:174)<br>`TestLifecycle_StartNotFound` (test/e2e/workspace/lifecycle_sm_test.go:189) |  |
| ERR-012 | waived | - | workspace.invalid_state — partially covered by invalid transition tests but exact error code mapping requires deeper assertion. |
| ERR-013 | covered | `TestErrors_CreateMissingRequiredFields` (test/e2e/workspace/errors_test.go:47) |  |
| ERR-014 | covered | `TestLifecycle_ForkRequiresChildRef` (test/e2e/workspace/lifecycle_sm_test.go:204) |  |
| ERR-015 | waived | - | workspace.no_snapshot — requires a workspace with no snapshot on restore; edge case. |
| ERR-022 | waived | - | workspace.fork_missing_ref — covered by TestLifecycle_ForkRequiresChildRef but error code mapping is not explicitly asserted. |
| ERR-023 | waived | - | workspace.restore no snapshot — same as ERR-015. |
| ERR-025 | waived | - | Workspace error not explicitly triggered in e2e. |
| ERR-040 | covered | `TestProject_DuplicateName` (test/e2e/project/project_test.go:97) |  |
| ERR-041 | waived | - | project.invalid_spec — requires empty repoUrl which is hard to trigger after client-side validation. |
| ERR-042 | waived | - | project.not_found — partially covered by TestProject but exact error code not asserted in e2e. |
| ERR-045 | waived | - | Project error not explicitly triggered in e2e. |
| ERR-050 | covered | `TestSpotlight_WorkspaceNotFound` (test/e2e/spotlight/spotlight_test.go:120) |  |
| ERR-051 | covered | `TestSpotlight_PortConflict` (test/e2e/spotlight/spotlight_behavioral_test.go:208) |  |
| ERR-052 | waived | - | spotlight.not_found — requires removing an already-removed forward. |
| ERR-055 | waived | - | Spotlight error not explicitly triggered in e2e. |
| ERR-060 | covered | `TestPTY_SessionNotFound` (test/e2e/pty/pty_test.go:168) |  |
| ERR-061 | waived | - | pty.not_found — covered by TestPTY_SessionNotFound but exact error code mapping not asserted. |
| ERR-065 | waived | - | PTY error not explicitly triggered in e2e. |
| ERR-070 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| ERR-071 | covered | `TestAuthRelay` (test/e2e/auth/relay_test.go:12) |  |
| ERR-072 | waived | - | auth.expired — requires time manipulation or waiting for TTL expiry. |
| ERR-075 | waived | - | Auth error not explicitly triggered in e2e. |
| ERR-080 | waived | - | General rule: exit code 0 definition. |
| ERR-081 | covered | `TestCLI_DaemonUnreachable` (test/e2e/cli/cli_test.go:151) |  |
| ERR-082 | covered | `TestCLI_UnknownSubcommand` (test/e2e/cli/cli_test.go:135) |  |
| INV-001 | waived | - | Workspace ID uniqueness is guaranteed by UUID generation; not testable in e2e. |
| INV-002 | covered | `TestErrors_DuplicateWorkspaceName` (test/e2e/workspace/errors_test.go:146) |  |
| INV-003 | covered | `TestProject` (test/e2e/project/project_test.go:12)<br>`TestProjectRepoDedup` (test/e2e/workspace/idempotency_test.go:83) |  |
| INV-004 | covered | `TestProject` (test/e2e/project/project_test.go:12)<br>`TestProject_DuplicateName` (test/e2e/project/project_test.go:97) |  |
| INV-005 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| INV-006 | waived | - | workspace.list no duplicates — guaranteed by database primary key; not testable in e2e. |
| INV-007 | covered | `TestIntegration_CreateAndGetLifecycle` (internal/app/workspace/integration_test.go:29)<br>`TestLifecycle_StartAndStop` (test/e2e/workspace/lifecycle_sm_test.go:62)<br>`TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| INV-008 | covered | `TestIntegration_CreateAndGetLifecycle` (internal/app/workspace/integration_test.go:29)<br>`TestLifecycle_StartAndStop` (test/e2e/workspace/lifecycle_sm_test.go:62)<br>`TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| INV-009 | covered | `TestIntegration_CreateAndGetLifecycle` (internal/app/workspace/integration_test.go:29)<br>`TestLifecycle_RemoveNotInList` (test/e2e/workspace/lifecycle_sm_test.go:143)<br>`TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| INV-010 | covered | `TestWorkspaceFork` (test/e2e/workspace/fork_test.go:13) |  |
| INV-011 | covered | `TestIntegration_CreateAndGetLifecycle` (internal/app/workspace/integration_test.go:29)<br>`TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| INV-012 | covered | `TestErrors_InvalidStateTransitions` (test/e2e/workspace/errors_test.go:171) |  |
| INV-013 | covered | `TestErrors_InvalidStateTransitions` (test/e2e/workspace/errors_test.go:171) |  |
| INV-014 | covered | `TestErrors_RemoveAlreadyRemoved` (test/e2e/workspace/errors_test.go:215) |  |
| INV-015 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| INV-016 | covered | `TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146)<br>`TestErrors_SpotlightStopUnknownWorkspace` (test/e2e/workspace/errors_test.go:108) |  |
| INV-017 | waived | - | workspace.ports.remove unknown forward — requires racing removal or specific edge case. |
| INV-018 | covered | `TestErrors_DuplicateWorkspaceName` (test/e2e/workspace/errors_test.go:146) |  |
| INV-019 | waived | - | Concurrent start serialization requires racing requests; tested at unit level. |
| INV-020 | waived | - | Snapshot isolation is a database property; tested at unit level. |
| INV-021 | waived | - | Concurrent fork behavior is implementation-defined by spec. |
| INV-022 | waived | - | spotlight.stop listener closure is an internal implementation detail. |
| INV-023 | waived | - | Daemon shutdown spotlight closure is an internal implementation detail. |
| INV-024 | waived | - | Daemon shutdown socket removal is an internal implementation detail. |
| INV-025 | covered | `TestPTY_SessionNotPersisted` (test/e2e/pty/pty_test.go:194) |  |
| INV-026 | waived | - | PTY exit notification delivery is best tested at unit level with mock processes. |
| INV-027 | waived | - | Removed workspace cleanup is an internal implementation detail. |
| PRJ-001 | waived | - | Conceptual clause: definition of a project. |
| PRJ-002 | waived | - | Conceptual clause: project ID format. |
| PRJ-003 | covered | `TestProjectRepoDedup` (test/e2e/workspace/idempotency_test.go:83) |  |
| PRJ-004 | waived | - | Conceptual clause: repoUrl non-empty requirement. |
| PRJ-005 | waived | - | Conceptual clause: rootPath definition. |
| PRJ-006 | waived | - | Conceptual clause: project config defaults. |
| PRJ-010 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-011 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-012 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-013 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-014 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-015 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-016 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-017 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-018 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-019 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-020 | covered | `TestProject` (test/e2e/project/project_test.go:12) |  |
| PRJ-030 | covered | `TestProject` (test/e2e/project/project_test.go:12)<br>`TestProjectRepoDedup` (test/e2e/workspace/idempotency_test.go:83) |  |
| PTY-001 | covered | `TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| PTY-002 | covered | `TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| PTY-003 | covered | `TestPTY_SessionNotPersisted` (test/e2e/pty/pty_test.go:194) |  |
| PTY-004 | waived | - | Internal implementation: pty.create pre-condition for running workspace is covered by error tests but the exact spec clause is an implementation detail. |
| PTY-005 | covered | `TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| PTY-006 | covered | `TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| PTY-007 | covered | `TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| PTY-008 | waived | - | Internal implementation: vsock agent connection protocol. |
| PTY-009 | covered | `TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| PTY-010 | covered | `TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-011 | covered | `TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-012 | covered | `TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-013 | covered | `TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-014 | covered | `TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-015 | waived | - | Internal implementation: libkrun guest PTY creation via agent. |
| PTY-016 | covered | `TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-017 | covered | `TestPTY_ListSession` (test/e2e/pty/pty_behavioral_test.go:159)<br>`TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-018 | covered | `TestPTY_ListSession` (test/e2e/pty/pty_behavioral_test.go:159)<br>`TestPTY` (test/e2e/pty/pty_test.go:12) |  |
| PTY-019 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-020 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-021 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-022 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-023 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-024 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-025 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-026 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-027 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-028 | covered | `TestPTY_Operations` (test/e2e/pty/pty_test.go:91) |  |
| PTY-029 | waived | - | Internal implementation: pty.reattach request shape; requires WebSocket/mux for push notifications. |
| PTY-030 | waived | - | Internal implementation: pty.reattach notification redirection; requires WebSocket/mux. |
| PTY-031 | waived | - | Internal implementation: pty.reattach scrollback replay; requires WebSocket/mux. |
| PTY-032 | waived | - | Internal implementation: push notification types and delivery; requires WebSocket/mux connection. |
| PTY-033 | waived | - | Internal implementation: SessionInfo domain object field definitions; partially verified by pty.create response. |
| RPC-001 | waived | - | Transport detail: Unix socket binding. |
| RPC-002 | waived | - | Transport detail: independent connection handling. |
| RPC-003 | waived | - | Transport detail: newline-delimited JSON-RPC. |
| RPC-004 | waived | - | Transport detail: Unix socket has no auth. |
| RPC-005 | waived | - | Transport detail: socket removal on clean shutdown. |
| RPC-006 | waived | - | Transport detail: stale socket removal. |
| RPC-007 | waived | - | Transport detail: Unix socket push notifications. |
| RPC-008 | waived | - | Transport detail: HTTP server startup. |
| RPC-009 | covered | `TestProtocol_Healthz` (test/e2e/workspace/protocol_test.go:17) |  |
| RPC-010 | covered | `TestProtocol_Version` (test/e2e/workspace/protocol_test.go:41) |  |
| RPC-011 | waived | - | Transport detail: WebSocket upgrade handshake. |
| RPC-012 | waived | - | Transport detail: WebSocket bearer token rejection. |
| RPC-013 | waived | - | Transport detail: WebSocket text frame encoding. |
| RPC-014 | waived | - | Transport detail: TLS mode definitions. |
| RPC-015 | waived | - | Transport detail: non-loopback TLS enforcement. |
| RPC-016 | waived | - | Transport detail: token resolution order. |
| RPC-017 | waived | - | Transport detail: JSON-RPC request fields. |
| RPC-018 | waived | - | Transport detail: JSON-RPC response fields. |
| RPC-019 | waived | - | Transport detail: JSON-RPC error object fields. |
| RPC-020 | covered | `TestErrors_MethodNotFound` (test/e2e/workspace/errors_test.go:119) |  |
| RPC-021 | waived | - | Transport detail: null result prohibition. |
| RPC-022 | waived | - | Transport detail: push notification JSON structure. |
| RPC-023 | waived | - | Transport detail: active push notification methods list. |
| RPC-024 | waived | - | Transport detail: push delivery on WebSocket only. |
| RPC-025 | waived | - | Transport detail: concurrent request handling. |
| RPC-026 | waived | - | Transport detail: MuxConn internal implementation. |
| RPC-027 | waived | - | Transport detail: MuxConn is internal CLI detail. |
| RPC-028 | waived | - | Transport detail: mux pty.data/pty.exit delivery. |
| RPC-029 | waived | - | Transport detail: workspace.discover-ports array response. |
| SPOT-001 | waived | - | Conceptual clause: definition of spotlight. |
| SPOT-002 | waived | - | Conceptual clause: forward ID definition. |
| SPOT-003 | waived | - | Conceptual clause: forward workspace association. |
| SPOT-004 | waived | - | Conceptual clause: localPort vs remotePort definition. |
| SPOT-005 | waived | - | Conceptual clause: process-sandbox binding behavior. |
| SPOT-006 | waived | - | Conceptual clause: libkrun VM vsock behavior. |
| SPOT-007 | waived | - | Conceptual clause: forward state enum. |
| SPOT-008 | waived | - | Conceptual clause: spotlight.stop atomicity description. |
| SPOT-009 | waived | - | Conceptual clause: client persistence behavior. |
| SPOT-010 | covered | `TestSpotlight_WorkspacePortsAlias` (test/e2e/spotlight/spotlight_behavioral_test.go:246) |  |
| SPOT-011 | covered | `TestSpotlight_WorkspacePortsAlias` (test/e2e/spotlight/spotlight_behavioral_test.go:246) |  |
| SPOT-012 | covered | `TestSpotlight_WorkspacePortsAlias` (test/e2e/spotlight/spotlight_behavioral_test.go:246) |  |
| SPOT-013 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76) |  |
| SPOT-014 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76) |  |
| SPOT-015 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76) |  |
| SPOT-016 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76) |  |
| SPOT-017 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146) |  |
| SPOT-018 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146) |  |
| SPOT-019 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146) |  |
| SPOT-020 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146) |  |
| SPOT-021 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146) |  |
| SPOT-022 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146) |  |
| SPOT-023 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestSpotlight_ListAndStop` (test/e2e/spotlight/spotlight_behavioral_test.go:146) |  |
| SPOT-024 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-025 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-026 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-027 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-028 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-029 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-030 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-031 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-032 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-033 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-034 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| SPOT-035 | covered | `TestSpotlight` (test/e2e/spotlight/spotlight_test.go:12) |  |
| VM-001 | covered | `TestCLI_WorkspaceShellAndExec` (test/e2e/cli/cli_test.go:62)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| VM-002 | covered | `TestCLI_WorkspaceShellAndExec` (test/e2e/cli/cli_test.go:62)<br>`TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42) |  |
| VM-003 | covered | `TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42) |  |
| VM-004 | covered | `TestCLI_WorkspaceShellAndExec` (test/e2e/cli/cli_test.go:62) |  |
| VM-005 | covered | `TestCLI_WorkspaceShellAndExec` (test/e2e/cli/cli_test.go:62)<br>`TestCLI_WorkspaceForkAndRestore` (test/e2e/cli/fork_restore_test.go:14)<br>`TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| VM-006 | waived | - | Internal implementation: daemon restart state recovery. |
| VM-007 | waived | - | Internal implementation: workspace-local tooling bootstrap path. |
| VM-008 | waived | - | Internal implementation: workspace-local tool installability. |
| VM-PROOF-001 | covered | `TestCLI_WorkspaceShellAndExec` (test/e2e/cli/cli_test.go:62)<br>`TestPTY_ExecEcho` (test/e2e/pty/pty_behavioral_test.go:62)<br>`TestPTY_ShellNonInteractiveScript` (test/e2e/pty/pty_behavioral_test.go:141) |  |
| VM-PROOF-002 | covered | `TestSpotlight_TCPProxyTraffic` (test/e2e/spotlight/spotlight_behavioral_test.go:76)<br>`TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| VM-PROOF-003 | waived | - | Requires deterministic daemon restart harness coverage for VM workspaces; tracked in child PRD 06 TD follow-up. |
| VM-PROOF-004 | covered | `TestCLI_WorkspaceForkAndRestore` (test/e2e/cli/fork_restore_test.go:14) |  |
| VM-PROOF-005 | covered | `TestCLI_WorkspaceShellAndExec` (test/e2e/cli/cli_test.go:62)<br>`TestPTY_ExecPWD` (test/e2e/pty/pty_behavioral_test.go:42) |  |
| VM-PROOF-006 | covered | `TestVMProof_GuestCLITools` (test/e2e/vmproof/tools_test.go:55) |  |
| WS-001 | waived | - | Conceptual clause: workspace definition. |
| WS-002 | waived | - | Conceptual clause: workspace ID uniqueness. |
| WS-003 | waived | - | Conceptual clause: workspace ID non-reuse. |
| WS-004 | waived | - | Conceptual clause: workspaceName uniqueness. |
| WS-005 | covered | `TestWorkspaceIdempotency` (test/e2e/workspace/idempotency_test.go:15) |  |
| WS-006 | covered | `TestWorkspaceIdempotency` (test/e2e/workspace/idempotency_test.go:15) |  |
| WS-007 | waived | - | Conceptual clause: parentWorkspaceId definition. |
| WS-008 | waived | - | Conceptual clause: backend enum definition. |
| WS-009 | waived | - | Conceptual clause: policy definition. |
| WS-010 | waived | - | Conceptual clause: created state definition. |
| WS-011 | waived | - | Conceptual clause: starting state definition. |
| WS-012 | waived | - | Conceptual clause: running state definition. |
| WS-013 | waived | - | Conceptual clause: paused state definition. |
| WS-014 | waived | - | Conceptual clause: stopped state definition. |
| WS-015 | waived | - | Conceptual clause: restored state definition. |
| WS-016 | waived | - | Conceptual clause: state exclusivity. |
| WS-017 | covered | `TestLifecycle_StartAndStop` (test/e2e/workspace/lifecycle_sm_test.go:62) |  |
| WS-018 | covered | `TestLifecycle_StartAndStop` (test/e2e/workspace/lifecycle_sm_test.go:62) |  |
| WS-019 | waived | - | starting -> created failure transition is an internal implementation detail. |
| WS-020 | covered | `TestLifecycle_StartAndStop` (test/e2e/workspace/lifecycle_sm_test.go:62) |  |
| WS-021 | covered | `TestLifecycle_RestoreFromStopped` (test/e2e/workspace/lifecycle_sm_test.go:114) |  |
| WS-022 | covered | `TestLifecycle_RemoveNotInList` (test/e2e/workspace/lifecycle_sm_test.go:143) |  |
| WS-023 | covered | `TestLifecycle_RemoveNotInList` (test/e2e/workspace/lifecycle_sm_test.go:143) |  |
| WS-024 | waived | - | restored -> running transition requires restored state setup. |
| WS-025 | waived | - | restored -> removed transition requires restored state setup. |
| WS-026 | covered | `TestErrors_InvalidStateTransitions` (test/e2e/workspace/errors_test.go:171) |  |
| WS-027 | covered | `TestErrors_InvalidStateTransitions` (test/e2e/workspace/errors_test.go:171) |  |
| WS-028 | covered | `TestErrors_InvalidStateTransitions` (test/e2e/workspace/errors_test.go:171) |  |
| WS-029 | waived | - | General illegal transition rule covered by specific transition tests. |
| WS-030 | covered | `TestWorkspaceFork` (test/e2e/workspace/fork_test.go:13) |  |
| WS-031 | covered | `TestWorkspaceFork` (test/e2e/workspace/fork_test.go:13) |  |
| WS-032 | covered | `TestWorkspaceFork` (test/e2e/workspace/fork_test.go:13) |  |
| WS-033 | covered | `TestWorkspaceFork` (test/e2e/workspace/fork_test.go:13) |  |
| WS-034 | covered | `TestLifecycle_ForkRequiresChildRef` (test/e2e/workspace/lifecycle_sm_test.go:204) |  |
| WS-040 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-041 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| WS-042 | waived | - | workspace.create unique name pre-condition is covered by TestErrors_DuplicateWorkspaceName but the exact spec clause mapping is implicit. |
| WS-043 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-044 | covered | `TestErrors_CreateMissingRequiredFields` (test/e2e/workspace/errors_test.go:47) |  |
| WS-045 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12) |  |
| WS-046 | covered | `TestLifecycle_NotFound` (test/e2e/workspace/lifecycle_sm_test.go:174) |  |
| WS-047 | covered | `TestLifecycle_NotFound` (test/e2e/workspace/lifecycle_sm_test.go:174) |  |
| WS-048 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-049 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-050 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-051 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-052 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-053 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-054 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-055 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-056 | covered | `TestWorkspaceLifecycle` (test/e2e/workspace/lifecycle_test.go:12)<br>`TestProtocol_WorkflowRoundTrip` (test/e2e/workspace/protocol_test.go:108) |  |
| WS-057 | waived | - | workspace.fork request shape. |
| WS-058 | waived | - | workspace.fork childRef required. |
| WS-059 | waived | - | workspace.fork pre-condition (source running). |
| WS-060 | covered | `TestWorkspaceFork` (test/e2e/workspace/fork_test.go:13) |  |
| WS-061 | waived | - | workspace.fork auto-generated name. |
| WS-062 | covered | `TestWorkspaceFork` (test/e2e/workspace/fork_test.go:13) |  |
| WS-063 | covered | `TestLifecycle_RestoreFromStopped` (test/e2e/workspace/lifecycle_sm_test.go:114)<br>`TestWorkspaceRestore` (test/e2e/workspace/restore_test.go:17) |  |
| WS-064 | covered | `TestLifecycle_RestoreFromStopped` (test/e2e/workspace/lifecycle_sm_test.go:114)<br>`TestWorkspaceRestore` (test/e2e/workspace/restore_test.go:17) |  |
| WS-065 | covered | `TestLifecycle_RestoreFromStopped` (test/e2e/workspace/lifecycle_sm_test.go:114)<br>`TestWorkspaceRestore` (test/e2e/workspace/restore_test.go:17) |  |
| WS-066 | covered | `TestLifecycle_RestoreFromStopped` (test/e2e/workspace/lifecycle_sm_test.go:114)<br>`TestWorkspaceRestore` (test/e2e/workspace/restore_test.go:17) |  |
| WS-067 | covered | `TestLifecycle_ReadyState` (test/e2e/workspace/lifecycle_sm_test.go:92) |  |
| WS-068 | covered | `TestLifecycle_ReadyState` (test/e2e/workspace/lifecycle_sm_test.go:92) |  |
| WS-069 | waived | - | workspace.discover-ports request shape. |
| WS-070 | waived | - | workspace.discover-ports response shape. |
| WS-071 | waived | - | DiscoveredPort object shape. |
| WS-072 | waived | - | workspace.discover-ports empty array behavior. |
| WS-073 | waived | - | Workspace object JSON fields. |
| WS-074 | waived | - | workspace.sshcheck request. |
| WS-075 | waived | - | workspace.sshcheck response. |
| WS-076 | waived | - | workspace.sshcheck SSH connectivity check. |
| WS-077 | waived | - | workspace.serial-log request. |
| WS-078 | waived | - | workspace.serial-log response. |
| WS-079 | waived | - | workspace.serial-log VM-only behavior. |

Generated by `go run ./test/e2e/coverage --check`.
