import XCTest
import Foundation
@testable import NexusCore

// MARK: - Test helpers

/// Default daemon URL for integration tests.
/// Override via NEXUS_TEST_DAEMON_URL env var.
private let defaultDaemonURL = URL(string:
    ProcessInfo.processInfo.environment["NEXUS_TEST_DAEMON_URL"] ?? "ws://localhost:63987"
)!

/// Returns true if the Nexus daemon is accepting connections at the default URL.
func isDaemonRunning() -> Bool {
    guard var comps = URLComponents(url: defaultDaemonURL, resolvingAgainstBaseURL: false) else { return false }
    comps.scheme = comps.scheme == "wss" ? "https" : "http"
    comps.path = "/healthz"
    comps.query = nil
    guard let url = comps.url else { return false }

    var req = URLRequest(url: url)
    req.timeoutInterval = 2
    let sem = DispatchSemaphore(value: 0)
    var running = false
    let task = URLSession.shared.dataTask(with: req) { _, resp, _ in
        running = (resp as? HTTPURLResponse)?.statusCode == 200
        sem.signal()
    }
    task.resume()
    _ = sem.wait(timeout: .now() + 3.0)
    return running
}

/// Creates a WebSocketDaemonClient pointed at the default daemon URL.
func makeClient() -> WebSocketDaemonClient {
    let token = ProcessInfo.processInfo.environment["NEXUS_DAEMON_TOKEN"]
             ?? WebSocketDaemonClient.readToken()
    return WebSocketDaemonClient(daemonURL: defaultDaemonURL, token: token.isEmpty ? nil : token)
}

// MARK: - Workspace model unit tests (no daemon required)

final class WorkspaceModelTests: XCTestCase {

    func testDecodeFromDaemonJSON() throws {
        let json = """
        {
            "id": "ws-abc",
            "workspaceName": "auth-feature",
            "repo": "git@github.com:acme/api.git",
            "ref": "feat/oauth",
            "state": "running",
            "rootPath": "/home/user/ws",
            "agentProfile": "default",
            "repoId": "repo-api"
        }
        """.data(using: .utf8)!

        let ws = try JSONDecoder().decode(Workspace.self, from: json)
        XCTAssertEqual(ws.id, "ws-abc")
        XCTAssertEqual(ws.workspaceName, "auth-feature")
        XCTAssertEqual(ws.ref, "feat/oauth")
        XCTAssertEqual(ws.state, .running)
        XCTAssertEqual(ws.repoId, "repo-api")
        XCTAssertTrue(ws.state.isActive)
    }

    func testDecodeHandlesMissingOptionalFields() throws {
        let json = """
        {"id": "ws-min", "workspaceName": "minimal"}
        """.data(using: .utf8)!

        let ws = try JSONDecoder().decode(Workspace.self, from: json)
        XCTAssertEqual(ws.id, "ws-min")
        XCTAssertEqual(ws.ref, "main")            // default
        XCTAssertEqual(ws.state, .stopped)        // default
        XCTAssertNil(ws.repoId)
    }

    func testDecodeRuntimeBackendAndLabel() throws {
        let json = """
        {
            "id": "ws-1",
            "workspaceName": "dev",
            "ref": "main",
            "state": "running",
            "backend": "firecracker",
            "runtimeLabel": "backend=firecracker isolation=vm"
        }
        """.data(using: .utf8)!

        let ws = try JSONDecoder().decode(Workspace.self, from: json)
        XCTAssertEqual(ws.backend, "firecracker")
        XCTAssertEqual(ws.runtimeLabel, "backend=firecracker isolation=vm")
        XCTAssertEqual(ws.shortRuntimeBadge, "VM")
        XCTAssertEqual(ws.detailRuntimeLine, "backend=firecracker isolation=vm")
    }

    func testWorkspaceStatusDisplayNames() {
        XCTAssertEqual(WorkspaceStatus.running.displayName,  "Running")
        XCTAssertEqual(WorkspaceStatus.paused.displayName,   "Paused")
        XCTAssertEqual(WorkspaceStatus.stopped.displayName,  "Stopped")
        XCTAssertEqual(WorkspaceStatus.created.displayName,  "Ready")
        XCTAssertEqual(WorkspaceStatus.restored.displayName, "Running")
    }

    func testIsActiveStates() {
        XCTAssertTrue(WorkspaceStatus.running.isActive)
        XCTAssertTrue(WorkspaceStatus.restored.isActive)
        XCTAssertFalse(WorkspaceStatus.paused.isActive)
        XCTAssertFalse(WorkspaceStatus.stopped.isActive)
        XCTAssertFalse(WorkspaceStatus.created.isActive)
    }

    func testRepoGroupingFromRelations() {
        let workspaces = [
            Workspace(id: "ws-1", workspaceName: "a", repo: "git@gh/api.git",
                      state: .running, repoId: "r1"),
            Workspace(id: "ws-2", workspaceName: "b", repo: "git@gh/api.git",
                      state: .stopped, repoId: "r1"),
            Workspace(id: "ws-3", workspaceName: "c", repo: "git@gh/web.git",
                      state: .stopped, repoId: "r2"),
        ]
        let groups = [
            RelationsGroup(repoId: "r1", repo: "git@gh/api.git", displayName: "api",
                           nodes: [
                            RelationNode(workspaceId: "ws-1", workspaceName: "a", state: .running),
                            RelationNode(workspaceId: "ws-2", workspaceName: "b", state: .stopped),
                           ]),
            RelationsGroup(repoId: "r2", repo: "git@gh/web.git", displayName: "web",
                           nodes: [
                            RelationNode(workspaceId: "ws-3", workspaceName: "c", state: .stopped),
                           ]),
        ]

        let repos = Repo.fromRelations(groups, workspaces: workspaces)
        XCTAssertEqual(repos.count, 2)
        XCTAssertEqual(repos[0].id, "r1")
        XCTAssertEqual(repos[0].name, "api")
        XCTAssertEqual(repos[0].workspaces.count, 2)
        XCTAssertEqual(repos[1].id, "r2")
        XCTAssertEqual(repos[1].workspaces.count, 1)
    }

    func testRepoFallbackGroupingNoRelations() {
        let workspaces = [
            Workspace(id: "ws-1", workspaceName: "a", repoId: "r1"),
            Workspace(id: "ws-2", workspaceName: "b", repoId: "r1"),
            Workspace(id: "ws-3", workspaceName: "c", repoId: "r2"),
        ]
        let repos = Repo.fromRelations([], workspaces: workspaces)
        // Falls back to flat grouping — all under single group
        XCTAssertFalse(repos.isEmpty)
    }

    func testWorkspaceCreateSpecEncoding() throws {
        let spec = WorkspaceCreateSpec(
            repo: "git@github.com:acme/api.git",
            ref: "main",
            workspaceName: "test-ws"
        )
        let data = try JSONEncoder().encode(spec)
        let dict = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        XCTAssertEqual(dict?["repo"] as? String, "git@github.com:acme/api.git")
        XCTAssertEqual(dict?["workspaceName"] as? String, "test-ws")
        XCTAssertEqual(dict?["ref"] as? String, "main")
    }

    func testProjectDecode() throws {
        let json = """
        {
            "id": "proj-1",
            "name": "nexus",
            "repoUrl": "git@github.com:oursky/nexus.git",
            "rootPath": "/Users/me/nexus"
        }
        """.data(using: .utf8)!
        let project = try JSONDecoder().decode(Project.self, from: json)
        XCTAssertEqual(project.id, "proj-1")
        XCTAssertEqual(project.name, "nexus")
        XCTAssertEqual(project.primaryRepo, "git@github.com:oursky/nexus.git")
    }

    func testProjectFirstGrouping() {
        let project = Project(id: "proj-1", name: "nexus", primaryRepo: "/tmp/nexus", rootPath: "/tmp/nexus")
        let workspaces = [
            Workspace(id: "ws-1", workspaceName: "main", projectId: "proj-1"),
            Workspace(id: "ws-2", workspaceName: "feature", projectId: "proj-1"),
            Workspace(id: "ws-3", workspaceName: "other", projectId: "proj-2"),
        ]
        let repos = Repo.fromProjects([project], workspaces: workspaces)
        XCTAssertEqual(repos.count, 1)
        XCTAssertEqual(repos[0].id, "proj-1")
        XCTAssertEqual(repos[0].workspaces.count, 2)
    }

    func testForwardedPortURL() {
        let port = ForwardedPort(id: 3000)
        XCTAssertEqual(port.port, 3000)
        XCTAssertEqual(port.localURL.absoluteString, "http://localhost:3000")
    }
}

@MainActor
final class AppStateProjectFlowTests: XCTestCase {

    override func setUp() {
        super.setUp()
        DaemonProfileStore().save([
            DaemonProfile(profileId: "ut", name: "UT", port: 7777, isDefault: true, sshTarget: "test@localhost"),
        ])
    }

    override func tearDown() {
        DaemonProfileStore().save([])
        super.tearDown()
    }

    func testCreateProjectAutoCreatesRootSandbox() async {
        let client = MockDaemonClient()
        let appState = AppState(client: client)
        _ = await appState.createProject(repo: "/tmp/nexus")

        XCTAssertEqual(client.createdProjectRepo, "/tmp/nexus")
        XCTAssertNotNil(client.createdDaemonSpec)
        XCTAssertEqual(client.createdDaemonSpec?.projectId, "proj-1")
        XCTAssertEqual(client.createdDaemonSpec?.ref, "main")
        XCTAssertEqual(client.createdDaemonSpec?.workspaceName, "nexus")
        XCTAssertEqual(appState.selectedWorkspaceID, "ws-root")
    }

    func testCreateProjectReturnsNilWhenSandboxBootstrapFails() async {
        let client = MockDaemonClient()
        client.shouldFailCreateWorkspaceDaemon = true
        let appState = AppState(client: client)
        let created = await appState.createProject(repo: "/tmp/nexus")

        XCTAssertNil(created)
        XCTAssertEqual(client.createdProjectRepo, "/tmp/nexus")
        XCTAssertNotNil(appState.error)
    }

    func testEnsureProjectRootSandboxCreatesMissingRoot() async {
        let client = MockDaemonClient()
        client.projects = [Project(id: "proj-1", name: "nexus", primaryRepo: "/tmp/nexus", rootPath: "/tmp/nexus")]
        let appState = AppState(client: client)
        await appState.load()

        let root = await appState.ensureProjectRootSandbox(projectID: "proj-1")
        XCTAssertNotNil(root)
        XCTAssertEqual(root?.projectId, "proj-1")
        XCTAssertNotNil(client.createdDaemonSpec)
        XCTAssertEqual(client.createdDaemonSpec?.ref, "main")
    }

    func testEnsureProjectRootSandboxReturnsExistingRootWithoutCreate() async {
        let client = MockDaemonClient()
        client.projects = [Project(id: "proj-1", name: "nexus", primaryRepo: "/tmp/nexus", rootPath: "/tmp/nexus")]
        client.workspaces = [Workspace(id: "ws-existing-root", workspaceName: "nexus", projectId: "proj-1")]
        let appState = AppState(client: client)
        await appState.load()

        let root = await appState.ensureProjectRootSandbox(projectID: "proj-1")
        XCTAssertEqual(root?.id, "ws-existing-root")
        XCTAssertNil(client.createdDaemonSpec)
    }

    func testEnsureProjectRootSandboxCreatesRootWhenOnlyChildrenExist() async {
        let client = MockDaemonClient()
        client.projects = [Project(id: "proj-1", name: "nexus", primaryRepo: "/tmp/nexus", rootPath: "/tmp/nexus")]
        let childJSON = """
        {
            "id": "ws-child",
            "workspaceName": "feature-x",
            "projectId": "proj-1",
            "parentWorkspaceId": "ws-some-parent",
            "ref": "feature-x"
        }
        """.data(using: .utf8)!
        client.workspaces = [try! JSONDecoder().decode(Workspace.self, from: childJSON)]
        let appState = AppState(client: client)
        await appState.load()

        let root = await appState.ensureProjectRootSandbox(projectID: "proj-1")
        XCTAssertNotNil(root)
        XCTAssertEqual(root?.id, "ws-root")
        XCTAssertEqual(root?.parentWorkspaceId, nil)
        XCTAssertEqual(client.createdDaemonSpec?.projectId, "proj-1")
    }
}

private final class MockDaemonClient: DaemonClient, @unchecked Sendable {
    var createdProjectRepo: String?
    var createdDaemonSpec: WorkspaceDaemonCreateSpec?
    var shouldFailCreateWorkspaceDaemon = false
    var projects: [Project] = []
    var workspaces: [Workspace] = []

    func listProjects() async throws -> [Project] { projects }
    func createProject(repo: String) async throws -> Project {
        createdProjectRepo = repo
        let project = Project(id: "proj-1", name: "nexus", primaryRepo: repo, rootPath: repo)
        projects = [project]
        return project
    }
    func removeProject(id: String) async throws {
        projects.removeAll { $0.id == id }
    }
    func listWorkspaces() async throws -> [Workspace] { workspaces }
    func createWorkspace(spec: WorkspaceCreateSpec) async throws -> Workspace {
        Workspace(id: "ws-create", workspaceName: spec.workspaceName, repo: spec.repo, ref: spec.ref)
    }
    func createWorkspaceDaemon(spec: WorkspaceDaemonCreateSpec) async throws -> Workspace {
        createdDaemonSpec = spec
        if shouldFailCreateWorkspaceDaemon {
            throw NSError(domain: "MockDaemonClient", code: 1, userInfo: [NSLocalizedDescriptionKey: "workspace create failed"])
        }
        let ws = Workspace(id: "ws-root", workspaceName: spec.workspaceName, projectId: spec.projectId)
        workspaces.append(ws)
        return ws
    }
    func forkWorkspace(parentID: String, childName: String, childRef: String) async throws -> Workspace {
        Workspace(id: "ws-fork", workspaceName: childName, ref: childRef, projectId: "proj-1")
    }
    func startWorkspace(id: String) async throws {}
    func stopWorkspace(id: String) async throws {}
    func removeWorkspace(id: String) async throws {}
    func markWorkspaceReady(id: String) async throws {}
    func discoverPorts(workspaceID: String) async throws -> [[String: Any]] { [] }
    func spotlightStart(workspaceId: String, localPort: Int, remotePort: Int, protocolText: String?) async throws -> (targetHost: String, targetPort: Int) {
        ("127.0.0.1", remotePort)
    }
    func spotlightStopWorkspace(workspaceId: String) async throws {}
    func listPorts(workspaceId: String) async throws -> [ForwardedPort] { [] }
    func addPortForward(workspaceId: String, localPort: Int, remotePort: Int) async throws {}
    func removePortForward(workspaceId: String, forwardId: String) async throws {}
    func startTunnels(workspaceId: String) async throws -> TunnelStatus { TunnelStatus(active: false, activeWorkspaceId: "") }
    func stopTunnels(workspaceId: String) async throws -> TunnelStatus { TunnelStatus(active: false, activeWorkspaceId: "") }
    func tunnelStatus(workspaceId: String) async throws -> TunnelStatus { TunnelStatus(active: false, activeWorkspaceId: "") }
    func workspaceInfo(id: String) async throws -> WorkspaceInfo {
        WorkspaceInfo(workspaceId: id, workspacePath: "/workspace", ports: [])
    }
    func getDaemonSandboxResourceSettings() async throws -> SandboxResourceSettings {
        SandboxResourceSettings(defaultMemoryMiB: 2048, defaultVCPUs: 2, maxMemoryMiB: 8192, maxVCPUs: 8)
    }
    func updateDaemonSandboxResourceSettings(_ settings: SandboxResourceSettings) async throws -> SandboxResourceSettings {
        settings
    }
}

// MARK: - Integration tests (require running daemon)

final class DaemonConnectionTests: XCTestCase {

    var client: WebSocketDaemonClient!

    override func setUp() async throws {
        guard isDaemonRunning() else {
            throw XCTSkip("Nexus daemon not running at localhost:8080 — skipping integration tests")
        }
        client = makeClient()
    }

    override func tearDown() async throws {
        client?.disconnect()
        client = nil
    }

    func testListWorkspacesReturnsArray() async throws {
        let workspaces = try await client.listWorkspaces()
        // Valid response may be empty — just assert it parsed
        XCTAssertNotNil(workspaces)
    }

}

final class WorkspaceLifecycleTests: XCTestCase {

    var client: WebSocketDaemonClient!
    var createdID: String?

    override func setUp() async throws {
        guard isDaemonRunning() else {
            throw XCTSkip("Nexus daemon not running — skipping lifecycle tests")
        }
        client = makeClient()
    }

    override func tearDown() async throws {
        if let id = createdID {
            try? await client.removeWorkspace(id: id)
        }
        client?.disconnect()
        client = nil
    }

    // Test create + remove using the local nexus repo (which always exists on dev machines)
    func testCreateAndRemove() async throws {
        let repoPath = ProcessInfo.processInfo.environment["NEXUS_TEST_REPO"]
                    ?? FileManager.default.homeDirectoryForCurrentUser
                         .appendingPathComponent("magic/nexus").path
        guard FileManager.default.fileExists(atPath: repoPath) else {
            throw XCTSkip("No test repo at \(repoPath) — set NEXUS_TEST_REPO env var")
        }

        let spec = WorkspaceCreateSpec(
            repo: repoPath,
            ref: "main",
            workspaceName: "e2e-test-\(Int(Date().timeIntervalSince1970))"
        )
        let ws = try await client.createWorkspace(spec: spec)
        createdID = ws.id
        XCTAssertFalse(ws.id.isEmpty)
        XCTAssertEqual(ws.workspaceName, spec.workspaceName)

        // Verify it appears in the list
        let list = try await client.listWorkspaces()
        XCTAssertTrue(list.contains { $0.id == ws.id }, "New workspace should appear in list")

        // Remove
        try await client.removeWorkspace(id: ws.id)
        createdID = nil
        let afterRemove = try await client.listWorkspaces()
        XCTAssertFalse(afterRemove.contains { $0.id == ws.id }, "Removed workspace should not appear")
    }

    // Test stop/start lifecycle on a workspace that is currently running.
    func testStopAndStartRunningWorkspace() async throws {
        let workspaces = try await client.listWorkspaces()
        guard let running = workspaces.first(where: { $0.state == .running }) else {
            throw XCTSkip("No running workspaces to test stop/start lifecycle")
        }

        try await client.stopWorkspace(id: running.id)
        let afterStop = try await client.listWorkspaces()
        let stopped = afterStop.first { $0.id == running.id }
        XCTAssertNotNil(stopped)
        XCTAssertEqual(stopped?.state, .stopped)

        // Restore state
        try await client.startWorkspace(id: running.id)
    }
}

final class PortDetectionTests: XCTestCase {

    var client: WebSocketDaemonClient!

    override func setUp() async throws {
        guard isDaemonRunning() else {
            throw XCTSkip("Nexus daemon not running at localhost:8080")
        }
        client = makeClient()
    }

    override func tearDown() async throws {
        client?.disconnect()
        client = nil
    }

    func testListPortsForNonexistentWorkspace() async throws {
        // Daemon should return empty array, not throw
        let ports = try await client.listPorts(workspaceId: "ws-does-not-exist")
        XCTAssertEqual(ports, [])
    }

    func testListPortsForRunningWorkspace() async throws {
        let workspaces = try await client.listWorkspaces()
        guard let running = workspaces.first(where: { $0.state == .running }) else {
            throw XCTSkip("No running workspaces to test port detection")
        }
        // Just assert it doesn't throw and returns an array
        let ports = try await client.listPorts(workspaceId: running.id)
        XCTAssertNotNil(ports)
        for port in ports {
            XCTAssertGreaterThan(port.port, 0)
            XCTAssertLessThan(port.port, 65536)
        }
    }
}

final class RemoteEditorURLTests: XCTestCase {

    func testFolderURLCursor() {
        let u = RemoteEditorURLBuilder.folderURL(app: .cursor, sshTarget: "dev@box", absoluteRemotePath: "/home/dev/proj")
        XCTAssertEqual(u?.absoluteString, "cursor://vscode-remote/ssh-remote+dev@box/home/dev/proj")
    }

    func testFolderURLVSCode() {
        let u = RemoteEditorURLBuilder.folderURL(app: .vscode, sshTarget: "dev@box", absoluteRemotePath: "/home/dev/proj")
        XCTAssertEqual(u?.absoluteString, "vscode://vscode-remote/ssh-remote+dev@box/home/dev/proj")
    }

    func testAllowsWorkspacePathForVMAlias() {
        let u = RemoteEditorURLBuilder.folderURL(app: .cursor, sshTarget: "nexus-vm-ws-1", absoluteRemotePath: "/workspace")
        XCTAssertEqual(u?.absoluteString, "cursor://vscode-remote/ssh-remote+nexus-vm-ws-1/workspace")
    }

    func testRejectsEmptySSH() {
        XCTAssertNil(RemoteEditorURLBuilder.folderURL(app: .cursor, sshTarget: " ", absoluteRemotePath: "/a"))
    }

    func testEncodesSpaceInPath() {
        let u = RemoteEditorURLBuilder.folderURL(app: .cursor, sshTarget: "u@h", absoluteRemotePath: "/a/b c/d")
        XCTAssertNotNil(u)
        XCTAssertTrue(u?.absoluteString.contains("b%20c") ?? false, "got \(u?.absoluteString ?? "")")
    }
}

final class WorkspaceRemoteSSHPathTests: XCTestCase {

    func testRemoteSSHRepoPathPrefersRepo() {
        let ws = Workspace(
            id: "ws-1",
            workspaceName: "w",
            repo: "/home/u/mirror",
            ref: "main",
            state: .running,
            rootPath: "/alt",
            agentProfile: "default"
        )
        XCTAssertEqual(ws.remoteSSHRepoPath, "/home/u/mirror")
    }

    func testRemoteSSHRepoPathFallsBackToRootPath() {
        let ws = Workspace(
            id: "ws-1",
            workspaceName: "w",
            repo: "",
            ref: "main",
            state: .running,
            rootPath: "/var/repo",
            agentProfile: "default"
        )
        XCTAssertEqual(ws.remoteSSHRepoPath, "/var/repo")
    }

    func testRemoteSSHRepoPathSkipsVMWorkspace() {
        let ws = Workspace(
            id: "ws-1",
            workspaceName: "w",
            repo: "/workspace",
            ref: "main",
            state: .running,
            rootPath: "",
            agentProfile: "default"
        )
        XCTAssertNil(ws.remoteSSHRepoPath)
    }

    func testRemoteSSHFolderOpenUsesGuestForFirecracker() {
        let ws = Workspace(
            id: "ws-abc",
            workspaceName: "w",
            repo: "/home/u/mirror",
            ref: "main",
            state: .running,
            rootPath: "",
            agentProfile: "default",
            backend: "firecracker",
            guestIp: "172.26.1.2"
        )
        let spec = ws.remoteSSHFolderOpen(jumpHost: "dev@linux", identityFile: nil)
        XCTAssertEqual(spec?.remotePath, "/workspace")
        XCTAssertEqual(spec?.vmGuestIP, "172.26.1.2")
        XCTAssertEqual(spec?.sshHostForURI, "nexus-vm-ws-abc")
        XCTAssertEqual(spec?.proxyJump, "dev@linux")
    }

    func testRemoteSSHFolderOpenUsesHostPathForProcess() {
        let ws = Workspace(
            id: "ws-1",
            workspaceName: "w",
            repo: "/home/u/proj",
            ref: "main",
            state: .running,
            rootPath: "",
            agentProfile: "default",
            backend: "process"
        )
        let spec = ws.remoteSSHFolderOpen(jumpHost: "dev@linux", identityFile: nil)
        XCTAssertEqual(spec?.remotePath, "/home/u/proj")
        XCTAssertNil(spec?.vmGuestIP)
        XCTAssertEqual(spec?.sshHostForURI, "dev@linux")
    }
}

final class RelationsGroupingTests: XCTestCase {

    var client: WebSocketDaemonClient!

    override func setUp() async throws {
        guard isDaemonRunning() else {
            throw XCTSkip("Nexus daemon not running at localhost:8080")
        }
        client = makeClient()
    }

    override func tearDown() async throws {
        client?.disconnect()
        client = nil
    }

    func testRepoGroupingContainsAllWorkspaceIDs() async throws {
        let workspaces = try await client.listWorkspaces()
        let repos = Repo.grouping(workspaces)
        let groupedIDs = Set(repos.flatMap(\.workspaces).map(\.id))
        for ws in workspaces {
            XCTAssertTrue(groupedIDs.contains(ws.id), "grouping should include \(ws.id)")
        }
    }

    func testRepoDisplayNamesAreNonEmptyWhenProjectsExist() async throws {
        let workspaces = try await client.listWorkspaces()
        let projects = try await client.listProjects()
        let repos = Repo.fromProjects(projects, workspaces: workspaces)
        for repo in repos {
            XCTAssertFalse(repo.name.isEmpty, "Repo '\(repo.id)' should have a non-empty display name")
        }
    }
}
