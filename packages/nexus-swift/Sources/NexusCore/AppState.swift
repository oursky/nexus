import Foundation
import Combine
import os

/// Root app state — owns the daemon client and drives all views.
/// Always connects to the real daemon. If the daemon isn't running,
/// connectionState reflects .disconnected and an error message is set.
@MainActor
public final class AppState: ObservableObject {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "AppState")

    // MARK: - PTY state (tracked for XCUITest via sidebar accessibility markers)

    public enum PTYState {
        case idle    // workspace stopped / no workspace selected
        case active  // PTY session open
        case error   // PTY failed
    }

    @Published public var ptyState: PTYState = .idle
    // Set by DaemonPTYTerminalView to re-focus the terminal NSView when the
    // sidebar terminal_view button is clicked in XCUITest.
    public var refocusTerminalAction: (() -> Void)?

    public func refocusTerminal() { refocusTerminalAction?() }

    /// Live terminal title from shell escape sequences (e.g. `\033]0;…\007`).
    /// Nil when no PTY is active or the shell has not set a title.
    @Published public var terminalTitle: String?

    /// Live working directory reported by the shell (OSC 7 / `hostCurrentDirectoryUpdate`).
    /// Nil when not reported.
    @Published public var terminalDirectory: String?

    // MARK: - Published state
    @Published public var repos: [Repo] = []
    @Published public var projects: [Project] = []
    @Published public var selectedWorkspaceID: String?
    @Published public var connectionState: ConnectionState = .disconnected
    @Published public var daemonStatus: DaemonStatus = .unknown
    @Published public var showNewWorkspace = false
    @Published public var newSandboxProjectID: String?
    @Published public var sidebarVisible = true
    @Published public var showInspector = true
    @Published public var error: String?

    // MARK: - Client
    public private(set) var client: any DaemonClient

    private var refreshTask: Task<Void, Never>?
    private var cachedProfile: DaemonProfile?
    private var tunnelManager: SSHTunnelManager?


    public init() {
        StartupTrace.beginSession()
        StartupTrace.checkpoint("app.init", "before client")

        let procEnv = ProcessInfo.processInfo.environment
        if let rawURL = procEnv["NEXUS_DAEMON_URL"], !rawURL.isEmpty,
           let daemonURL = URL(string: rawURL) {
            let token: String? = {
                let t = procEnv["NEXUS_DAEMON_TOKEN"] ?? ""
                return t.isEmpty ? nil : t
            }()
            self.cachedProfile = nil
            self.client = WebSocketDaemonClient(daemonURL: daemonURL, token: token)
            connectionState = .connecting
            StartupTrace.checkpoint("app.init", "env-var bypass; url=\(rawURL)")
            Task { await self.load() }
            startRefreshLoop()
            return
        }

        let profile = DaemonProfileStore().defaultProfile()
        self.cachedProfile = profile
        self.client = NullDaemonClient()
        connectionState = .starting
        StartupTrace.checkpoint("app.init", "after client; scheduling load")
        Task { await self.connectRemoteAndLoad() }
        startRefreshLoop()
    }

    // Designated for dependency injection in tests only
    public init(client: any DaemonClient) {
        StartupTrace.beginSession()
        StartupTrace.checkpoint("app.init.inject", "before storing client")
        self.client = client
        StartupTrace.checkpoint("app.init.inject", "load scheduled")
        Task { await self.load() }
    }

    private func applyLoadedWorkspaces(_ workspaces: [Workspace], relations: [RelationsGroup], projects: [Project]) {
        self.projects = projects
        let projectRepos = Repo.fromProjects(projects, workspaces: workspaces)
        repos = projectRepos.isEmpty ? Repo.fromRelations(relations, workspaces: workspaces) : projectRepos
        connectionState = .connected
        error = nil

        if selectedWorkspaceID == nil {
            selectedWorkspaceID = repos.first?.workspaces.first?.id
        }
    }

    // MARK: - Load

    /// Wall-clock cap for markWorkspaceReady / ports / tunnel fan-out (many actives can wedge the daemon).
    private static let workspaceEnrichmentDeadlineSeconds: UInt64 = 35
    /// Hard cap for the entire auto-start + first successful data fetch (independent of per-RPC timeouts).
    private static let startupDeadlineSeconds: UInt64 = 120

    public func load() async {
        connectionState = .connecting
        Self.logger.debug("load() started")
        StartupTrace.checkpoint("load.enter")
        do {
            async let wsFetch = client.listWorkspaces()
            async let relationsFetch = client.listRelations()
            var workspaces = try await wsFetch
            let relations = try await relationsFetch
            let projects = try await client.listProjects()

            // Phase 1 — connect the UI as soon as lists return (no per-workspace side effects yet).
            applyLoadedWorkspaces(workspaces, relations: relations, projects: projects)
            StartupTrace.checkpoint("load.phase1_ok", "workspaces=\(workspaces.count) projects=\(projects.count)")
            // Update daemon status from /version — best-effort, must not block phase 2.
            Task { await self.refreshDaemonStatus() }
            Self.logger.debug("load() phase-1 connected with \(workspaces.count, privacy: .public) workspaces")

            // Phase 2 — best-effort; must not block startup indefinitely or pin RAM on hung RPCs.
            do {
                StartupTrace.checkpoint("load.phase2_begin", "enrichment deadline \(Self.workspaceEnrichmentDeadlineSeconds)s")
                workspaces = try await AsyncDeadline.withSecondsOnMainActor(Self.workspaceEnrichmentDeadlineSeconds) {
                    await self.enrichActiveWorkspaceSideEffects(workspaces: workspaces)
                }
                applyLoadedWorkspaces(workspaces, relations: relations, projects: projects)
                StartupTrace.checkpoint("load.phase2_ok")
            } catch {
                if error is AsyncDeadlineError {
                    StartupTrace.checkpoint("load.phase2_deadline_skip")
                    Self.logger.warning("load() enrichment skipped: deadline \(Self.workspaceEnrichmentDeadlineSeconds, privacy: .public)s (ports/tunnels may be stale until next refresh)")
                } else {
                    throw error
                }
            }

            Self.logger.debug("load() finished with \(workspaces.count, privacy: .public) workspaces and \(projects.count, privacy: .public) projects")
        } catch {
            connectionState = .disconnected
            daemonStatus = .offline
            if self.error == nil {
                self.error = "Cannot reach daemon: \(error.localizedDescription)"
            }
            StartupTrace.checkpoint("load.failed", error.localizedDescription)
            Self.logger.error("load() failed: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// Per-active-workspace RPC fan-out (runs under a deadline in `load()`).
    private func enrichActiveWorkspaceSideEffects(workspaces: [Workspace]) async -> [Workspace] {
        var workspaces = workspaces
        var activeTunnelWorkspaceID = ""
        await withTaskGroup(of: (String, [ForwardedPort], String).self) { group in
            for ws in workspaces where ws.state.isActive {
                group.addTask { [c = self.client] in
                    try? await c.markWorkspaceReady(id: ws.id)
                    let ports = (try? await c.listPorts(workspaceId: ws.id)) ?? []
                    let status = (try? await c.tunnelStatus(workspaceId: ws.id))
                    return (ws.id, ports, status?.activeWorkspaceId ?? "")
                }
            }
            for await (id, ports, activeID) in group {
                if let idx = workspaces.firstIndex(where: { $0.id == id }) {
                    workspaces[idx].ports = ports
                }
                if !activeID.isEmpty { activeTunnelWorkspaceID = activeID }
            }
        }
        for i in workspaces.indices {
            workspaces[i].hasActiveTunnels = (workspaces[i].id == activeTunnelWorkspaceID)
        }
        return workspaces
    }

    // MARK: - Background refresh (4 s polling)

    private func startRefreshLoop() {
        refreshTask?.cancel()
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(4))
                guard !Task.isCancelled, let self else { break }
                // Never poll `load()` during initial handshake — it stacks concurrent RPCs,
                // duplicates WebSocket work, and grows memory while the UI stays on "Starting…".
                if self.connectionState == .starting || self.connectionState == .connecting {
                    continue
                }
                // Avoid hammering JSON-RPC when the daemon is too old (handled by restart/upgrade flows).
                if case .outdated = self.daemonStatus {
                    continue
                }
                await self.load()
            }
        }
    }

    // MARK: - Remote connection

    /// Fetches /version from the current client and updates daemonStatus.
    /// Best-effort: silently skips if the client is not a WebSocketDaemonClient.
    /// If /version is unreachable or returns an incompatible format but load() succeeded,
    /// falls back to a synthetic .running entry so the UI shows green instead of grey.
    private func refreshDaemonStatus() async {
        guard let wsClient = client as? WebSocketDaemonClient else { return }
        if let info = await wsClient.fetchDaemonInfo() {
            daemonStatus = info.isCompatible ? .running(info: info) : .outdated(running: info)
            StartupTrace.checkpoint("daemon.status.ok", "v=\(info.version) protocol=\(info.protocolVersion)")
        } else {
            // /version endpoint missing or returned unexpected JSON (e.g. old daemon).
            // We know the daemon is reachable because load() just succeeded — mark running
            // with a synthetic info so the status pill shows green.
            let synthetic = DaemonInfo(name: "nexus", version: "unknown", commit: "", builtAt: "", protocolVersion: DaemonInfo.requiredProtocol)
            daemonStatus = .running(info: synthetic)
            StartupTrace.checkpoint("daemon.status.fallback", "version endpoint unavailable; marking running")
        }
    }

    private func connectRemoteAndLoad() async {
        StartupTrace.checkpoint("remote.enter", "cachedProfile=\(cachedProfile != nil ? "yes" : "nil")")
        guard let profile = cachedProfile else {
            connectionState = .disconnected
            error = "No remote profile configured. Add one in Settings."
            StartupTrace.checkpoint("remote.noProfile")
            return
        }

        connectionState = .connecting
        StartupTrace.checkpoint("remote.tunnel.start", "sshTarget=\(profile.sshTarget ?? "nil") port=\(profile.port)")
        let mgr = SSHTunnelManager(profile: profile)
        self.tunnelManager = mgr
        let daemonURL: URL
        let resolvedToken: String
        do {
            let localPort = try await mgr.start()
            StartupTrace.checkpoint("remote.tunnel.ok", "localPort=\(localPort)")
            resolvedToken = try await mgr.fetchRemoteToken()
            StartupTrace.checkpoint("remote.token.ok", "tokenLen=\(resolvedToken.count)")
            guard let url = URL(string: "ws://127.0.0.1:\(localPort)") else {
                connectionState = .disconnected
                error = "Tunnel started but could not form local URL"
                return
            }
            daemonURL = url
        } catch {
            connectionState = .disconnected
            self.error = "SSH tunnel failed: \(error.localizedDescription)"
            StartupTrace.checkpoint("remote.tunnel.failed", error.localizedDescription)
            self.tunnelManager = nil
            return
        }

        client = WebSocketDaemonClient(daemonURL: daemonURL, token: resolvedToken.isEmpty ? nil : resolvedToken)
        connectionState = .connecting
        StartupTrace.checkpoint("remote.connect", daemonURL.absoluteString)
        do {
            try await AsyncDeadline.withSecondsOnMainActor(30) {
                await self.load()
            }
            self.updateProfileStatus(profileId: profile.profileId, status: .connected)
        } catch {
            if connectionState != .connected {
                connectionState = .disconnected
                self.error = "Remote daemon unreachable: \(daemonURL.host ?? ""):\(daemonURL.port ?? 0) — \(error.localizedDescription)"
                StartupTrace.checkpoint("remote.connect.failed", error.localizedDescription)
                Self.logger.error("connectRemoteAndLoad failed: \(error.localizedDescription, privacy: .public)")
                self.updateProfileStatus(profileId: profile.profileId, status: .unreachable)
            }
        }
    }

    /// Persists an updated `lastKnownStatus` for the given profile ID.
    private func updateProfileStatus(profileId: String, status: ProfileStatus) {
        let store = DaemonProfileStore()
        var profiles = store.load()
        guard let idx = profiles.firstIndex(where: { $0.profileId == profileId }) else { return }
        profiles[idx].lastKnownStatus = status
        store.save(profiles)
        // Refresh cachedProfile if it matches.
        if cachedProfile?.profileId == profileId {
            cachedProfile = profiles[idx]
        }
    }


    /// Re-reads the default profile and reconnects (e.g. after the user changes the active profile).
    public func reconnect() async {
        await tunnelManager?.stop()
        tunnelManager = nil
        let profile = DaemonProfileStore().defaultProfile()
        self.cachedProfile = profile
        connectionState = .starting
        await connectRemoteAndLoad()
    }

    /// Fast-path: three list RPCs only (no markWorkspaceReady / ports fan-out).
    private func attemptLoad() async throws {
        StartupTrace.checkpoint("attempt_load.enter")
        async let wsFetch = client.listWorkspaces()
        async let relationsFetch = client.listRelations()
        let workspaces = try await wsFetch
        let relations = try await relationsFetch
        let projects = try await client.listProjects()
        applyLoadedWorkspaces(workspaces, relations: relations, projects: projects)
    }

    // MARK: - Workspace actions

    public func createWorkspace(spec: WorkspaceCreateSpec) async {
        do {
            let ws = try await client.createWorkspace(spec: spec)
            await load()
            selectedWorkspaceID = ws.id
        } catch {
            self.error = error.localizedDescription
        }
    }

    public func createSandbox(request: SandboxCreateRequest) async {
        do {
            let ws = try await client.createSandbox(request: request)
            await load()
            selectedWorkspaceID = ws.id
            ConfigSyncManager.shared.startConfigSync(workspaceID: ws.id, backend: ws.backend)
        } catch {
            self.error = error.localizedDescription
        }
    }

    public func ensureProjectRootSandbox(projectID: String) async -> Workspace? {
        if let existing = projectRootSandbox(projectID: projectID) {
            ConfigSyncManager.shared.startConfigSync(workspaceID: existing.id, backend: existing.backend)
            return existing
        }
        if projects.isEmpty || !projects.contains(where: { $0.id == projectID }) {
            await load()
            if let existing = projectRootSandbox(projectID: projectID) {
                ConfigSyncManager.shared.startConfigSync(workspaceID: existing.id, backend: existing.backend)
                return existing
            }
        }
        guard let project = projects.first(where: { $0.id == projectID }) else {
            self.error = "Project not found: \(projectID)"
            return nil
        }
        do {
            _ = try await client.createSandbox(request: SandboxCreateRequest(
                projectId: projectID,
                targetBranch: "main",
                sourceBranch: nil,
                sourceWorkspaceId: nil,
                fresh: true,
                workspaceName: project.name
            ))
            await load()
            if let root = projectRootSandbox(projectID: projectID) {
                ConfigSyncManager.shared.startConfigSync(workspaceID: root.id, backend: root.backend)
                return root
            }
            self.error = "Project root sandbox creation did not appear in list"
            return nil
        } catch {
            await load()
            if let root = projectRootSandbox(projectID: projectID) {
                return root
            }
            self.error = error.localizedDescription
            return nil
        }
    }

    public func createProject(repo: String) async -> Project? {
        do {
            let project = try await client.createProject(repo: repo)
            await load()
            guard let rootSandbox = await ensureProjectRootSandbox(projectID: project.id) else {
                return nil
            }
            selectedWorkspaceID = rootSandbox.id
            return project
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    public func start(_ workspace: Workspace) async {
        await perform { try await self.client.startWorkspace(id: workspace.id) }
    }

    public func stop(_ workspace: Workspace) async {
        await perform { try await self.client.stopWorkspace(id: workspace.id) }
    }

    public func remove(_ workspace: Workspace) async {
        if selectedWorkspaceID == workspace.id { selectedWorkspaceID = nil }
        ConfigSyncManager.shared.stopConfigSync(workspaceID: workspace.id)
        await perform { try await self.client.removeWorkspace(id: workspace.id) }
    }

    public func addPort(_ port: Int, workspace: Workspace) async {
        await perform {
            try await self.client.addPort(workspaceId: workspace.id, port: port)
        }
    }

    public func removePort(_ port: Int, workspace: Workspace) async {
        await perform {
            try await self.client.removePort(workspaceId: workspace.id, port: port)
        }
    }

    public func startTunnels(_ workspace: Workspace) async {
        await perform {
            _ = try await self.client.startTunnels(workspaceId: workspace.id)
        }
    }

    public func stopTunnels(_ workspace: Workspace) async {
        await perform {
            _ = try await self.client.stopTunnels(workspaceId: workspace.id)
        }
    }

    private func perform(_ op: @escaping () async throws -> Void) async {
        do {
            try await op()
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Computed helpers

    public var selectedWorkspace: Workspace? {
        repos.flatMap(\.workspaces).first { $0.id == selectedWorkspaceID }
    }

    public var allWorkspaces: [Workspace] {
        repos.flatMap(\.workspaces)
    }

    private func projectRootSandbox(projectID: String) -> Workspace? {
        guard let repo = repos.first(where: { $0.id == projectID }) else { return nil }
        return repo.workspaces.first(where: { ($0.parentWorkspaceId ?? "").isEmpty })
    }
}

public enum ConnectionState: Equatable {
    case starting, disconnected, connecting, connected
}

/// The compatibility status of the running daemon.
public enum DaemonStatus: Equatable {
    case unknown
    case running(info: DaemonInfo)
    case outdated(running: DaemonInfo)  // protocolVersion < requiredProtocol
    case offline
}
