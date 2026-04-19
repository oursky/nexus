import Foundation

/// A no-op DaemonClient used as the initial placeholder in AppState before a real
/// connection is established. Every call throws immediately so the refresh loop
/// fails cleanly instead of hitting a stale hardcoded port.
public struct NullDaemonClient: DaemonClient {
    private static let err = RPCError(message: "no daemon connected")

    public init() {}

    public func listProjects() async throws -> [Project] { throw Self.err }
    public func createProject(repo: String) async throws -> Project { throw Self.err }
    public func listWorkspaces() async throws -> [Workspace] { throw Self.err }
    public func listRelations() async throws -> [RelationsGroup] { throw Self.err }
    public func createWorkspace(spec: WorkspaceCreateSpec) async throws -> Workspace { throw Self.err }
    public func createSandbox(request: SandboxCreateRequest) async throws -> Workspace { throw Self.err }
    public func startWorkspace(id: String) async throws { throw Self.err }
    public func stopWorkspace(id: String) async throws { throw Self.err }
    public func removeWorkspace(id: String) async throws { throw Self.err }
    public func markWorkspaceReady(id: String) async throws { throw Self.err }
    public func listPorts(workspaceId: String) async throws -> [ForwardedPort] { throw Self.err }
    public func addPort(workspaceId: String, port: Int) async throws { throw Self.err }
    public func removePort(workspaceId: String, port: Int) async throws { throw Self.err }
    public func startTunnels(workspaceId: String) async throws -> TunnelStatus { throw Self.err }
    public func stopTunnels(workspaceId: String) async throws -> TunnelStatus { throw Self.err }
    public func tunnelStatus(workspaceId: String) async throws -> TunnelStatus { throw Self.err }
    public func exec(workspaceId: String, command: String, args: [String]) async throws -> ExecOutput { throw Self.err }
    public func workspaceInfo(id: String) async throws -> WorkspaceInfo { throw Self.err }
    public func getDaemonSandboxResourceSettings() async throws -> SandboxResourceSettings { throw Self.err }
    public func updateDaemonSandboxResourceSettings(_ settings: SandboxResourceSettings) async throws -> SandboxResourceSettings { throw Self.err }
}
