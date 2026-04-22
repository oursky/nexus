import Foundation

/// Placeholder before the SSH tunnel + WebSocket client is installed.
public struct NullDaemonClient: DaemonClient {
    private static let err = RPCError(message: "no daemon connected")

    public init() {}

    public func listProjects() async throws -> [Project] { throw Self.err }
    public func createProject(repo: String) async throws -> Project { throw Self.err }
    public func removeProject(id: String) async throws { throw Self.err }
    public func listWorkspaces() async throws -> [Workspace] { throw Self.err }
    public func createWorkspace(spec: WorkspaceCreateSpec) async throws -> Workspace { throw Self.err }
    public func createWorkspaceDaemon(spec: WorkspaceDaemonCreateSpec) async throws -> Workspace { throw Self.err }
    public func forkWorkspace(parentID: String, childName: String, childRef: String) async throws -> Workspace { throw Self.err }
    public func startWorkspace(id: String) async throws { throw Self.err }
    public func stopWorkspace(id: String) async throws { throw Self.err }
    public func removeWorkspace(id: String) async throws { throw Self.err }
    public func markWorkspaceReady(id: String) async throws { throw Self.err }
    public func discoverPorts(workspaceID: String) async throws -> [[String: Any]] { throw Self.err }
    public func spotlightStart(workspaceId: String, localPort: Int, remotePort: Int, protocolText: String?) async throws -> (targetHost: String, targetPort: Int) { throw Self.err }
    public func spotlightStopWorkspace(workspaceId: String) async throws { throw Self.err }
    public func listPorts(workspaceId: String) async throws -> [ForwardedPort] { throw Self.err }
    public func addPortForward(workspaceId: String, localPort: Int, remotePort: Int) async throws { throw Self.err }
    public func removePortForward(workspaceId: String, forwardId: String) async throws { throw Self.err }
    public func startTunnels(workspaceId: String) async throws -> TunnelStatus { throw Self.err }
    public func stopTunnels(workspaceId: String) async throws -> TunnelStatus { throw Self.err }
    public func tunnelStatus(workspaceId: String) async throws -> TunnelStatus { throw Self.err }
    public func workspaceInfo(id: String) async throws -> WorkspaceInfo { throw Self.err }
    public func getDaemonSandboxResourceSettings() async throws -> SandboxResourceSettings { throw Self.err }
    public func updateDaemonSandboxResourceSettings(_ settings: SandboxResourceSettings) async throws -> SandboxResourceSettings { throw Self.err }
    public func checkVMSSH(workspaceId: String) async throws -> VMSSHCheckResult { throw Self.err }
}
