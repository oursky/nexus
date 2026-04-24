import Foundation

// MARK: - Protocol

/// Abstraction over the Nexus daemon RPC interface.
public protocol DaemonClient: Sendable {

    // ── Projects ─────────────────────────────────────────────────────
    func listProjects() async throws -> [Project]
    func createProject(repo: String) async throws -> Project
    func removeProject(id: String) async throws

    // ── Discovery ────────────────────────────────────────────────────
    func listWorkspaces() async throws -> [Workspace]

    // ── Lifecycle ────────────────────────────────────────────────────
    func createWorkspace(spec: WorkspaceCreateSpec) async throws -> Workspace
    /// Daemon `workspace.create` with a mirrored Linux `repo` path and optional `projectId`.
    func createWorkspaceDaemon(spec: WorkspaceDaemonCreateSpec) async throws -> Workspace
    func forkWorkspace(parentID: String, childName: String, childRef: String) async throws -> Workspace
    func startWorkspace(id: String) async throws
    func stopWorkspace(id: String) async throws
    func removeWorkspace(id: String) async throws

    // ── Ports ────────────────────────────────────────────────────────
    func markWorkspaceReady(id: String) async throws
    func discoverPorts(workspaceID: String) async throws -> [[String: Any]]
    func spotlightStart(workspaceId: String, localPort: Int, remotePort: Int, protocolText: String?) async throws -> (targetHost: String, targetPort: Int)
    func spotlightStopWorkspace(workspaceId: String) async throws
    func listPorts(workspaceId: String) async throws -> [ForwardedPort]
    func addPortForward(workspaceId: String, localPort: Int, remotePort: Int) async throws
    func removePortForward(workspaceId: String, forwardId: String) async throws
    func startTunnels(workspaceId: String) async throws -> TunnelStatus
    func stopTunnels(workspaceId: String) async throws -> TunnelStatus
    func tunnelStatus(workspaceId: String) async throws -> TunnelStatus

    // ── Workspace info ────────────────────────────────────────────────
    /// Returns rich workspace metadata including spotlight ports.
    func workspaceInfo(id: String) async throws -> WorkspaceInfo

    // ── Daemon settings ────────────────────────────────────────────────
    func getDaemonSandboxResourceSettings() async throws -> SandboxResourceSettings
    func updateDaemonSandboxResourceSettings(_ settings: SandboxResourceSettings) async throws -> SandboxResourceSettings

    // ── VM SSH diagnostics ─────────────────────────────────────────────
    /// Asks the daemon to test SSH connectivity from the engine host into the VM.
    func checkVMSSH(workspaceId: String) async throws -> VMSSHCheckResult

    // ── Diagnostics / Logs ─────────────────────────────────────────────
    /// Returns the last `lines` lines of the workspace's VM serial/console log.
    func workspaceSerialLog(workspaceId: String, lines: Int) async throws -> WorkspaceSerialLog
    /// Returns the last `lines` lines of the daemon process log.
    func daemonLogTail(lines: Int) async throws -> DaemonLogTail
}

/// Result of `workspace.sshcheck` — SSH connectivity test run from the engine host.
public struct VMSSHCheckResult: Sendable {
    public let ok: Bool
    public let guestIP: String
    public let whoami: String
    public let error: String
    public let stderr: String

    public init(ok: Bool, guestIP: String = "", whoami: String = "", error: String = "", stderr: String = "") {
        self.ok = ok
        self.guestIP = guestIP
        self.whoami = whoami
        self.error = error
        self.stderr = stderr
    }
}

public struct SandboxCreateRequest: Sendable {
    public let projectId: String
    public let targetBranch: String
    public let sourceBranch: String?
    public let sourceWorkspaceId: String?
    public let fresh: Bool
    public let workspaceName: String
    public let agentProfile: String
    public let backend: String

    public init(
        projectId: String,
        targetBranch: String = "main",
        sourceBranch: String? = nil,
        sourceWorkspaceId: String? = nil,
        fresh: Bool = false,
        workspaceName: String,
        agentProfile: String = "default",
        backend: String = ""
    ) {
        self.projectId = projectId
        self.targetBranch = targetBranch
        self.sourceBranch = sourceBranch
        self.sourceWorkspaceId = sourceWorkspaceId
        self.fresh = fresh
        self.workspaceName = workspaceName
        self.agentProfile = agentProfile
        self.backend = backend
    }
}

public struct TunnelStatus: Sendable {
    public let active: Bool
    public let activeWorkspaceId: String
}

/// Result of `workspace.serial-log` — VM console/serial log tail.
public struct WorkspaceSerialLog: Sendable {
    public let lines: [String]
    public let path: String
    public let available: Bool

    public init(lines: [String] = [], path: String = "", available: Bool = false) {
        self.lines = lines
        self.path = path
        self.available = available
    }
}

/// Result of `daemon.log.tail` — daemon process log tail.
public struct DaemonLogTail: Sendable {
    public let lines: [String]
    public let path: String

    public init(lines: [String] = [], path: String = "") {
        self.lines = lines
        self.path = path
    }
}

public struct SandboxResourceSettings: Sendable, Equatable {
    public let defaultMemoryMiB: Int
    public let defaultVCPUs: Int
    public let maxMemoryMiB: Int
    public let maxVCPUs: Int

    public init(defaultMemoryMiB: Int, defaultVCPUs: Int, maxMemoryMiB: Int, maxVCPUs: Int) {
        self.defaultMemoryMiB = defaultMemoryMiB
        self.defaultVCPUs = defaultVCPUs
        self.maxMemoryMiB = maxMemoryMiB
        self.maxVCPUs = maxVCPUs
    }
}
