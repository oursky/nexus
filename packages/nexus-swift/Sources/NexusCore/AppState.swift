import Foundation
import Combine
import os

/// Root app state — owns the daemon client and drives all views.
/// **Remote daemon only:** connects over SSH (local port forward) + WebSocket to the Linux host.
/// There is no embedded or localhost daemon path in production.
@MainActor
public final class AppState: ObservableObject {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "AppState")

    // MARK: - PTY state (sidebar accessibility markers for automation / assistive tech)

    public enum PTYState {
        case idle    // workspace stopped / no workspace selected
        case active  // PTY session open
        case error   // PTY failed
    }

    @Published public var ptyState: PTYState = .idle
    // Set by DaemonPTYTerminalView to re-focus the terminal NSView when the
    // sidebar terminal_view button is activated (e.g. accessibility).
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
    @Published public var createIntent: CreateIntent?
    @Published public var sidebarVisible = true
    @Published public var showInspector = true
    @Published public var error: String?

    // MARK: - Client
    public private(set) var client: any DaemonClient

    private var refreshTask: Task<Void, Never>?
    private var cachedProfile: DaemonProfile?
    private var tunnelManager: SSHTunnelManager?
    private var daemonLogStream: DaemonLogStream?


    public init() {
        StartupTrace.beginSession()
        StartupTrace.checkpoint("app.init", "before client")

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

    private func applyLoadedWorkspaces(_ workspaces: [Workspace], projects: [Project]) {
        self.projects = projects

        // Carry forward known ports/tunnel state into workspaces that arrive with empty ports
        // (phase-1 list-only snapshot) so the Ports panel never flashes "No detected ports"
        // during the gap between phase-1 and phase-2 enrichment every 4-second poll cycle.
        let existing: [String: Workspace] = repos
            .flatMap { $0.workspaces }
            .reduce(into: [:]) { $0[$1.id] = $1 }
        let carried: [Workspace] = workspaces.map { ws in
            guard ws.ports.isEmpty,
                  let prev = existing[ws.id],
                  !prev.ports.isEmpty else { return ws }
            var updated = ws
            updated.ports = prev.ports
            updated.hasActiveTunnels = prev.hasActiveTunnels
            return updated
        }

        let projectRepos = Repo.fromProjects(projects, workspaces: carried)
        repos = projectRepos.isEmpty ? Repo.grouping(carried) : projectRepos
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
            StartupTrace.checkpoint("load.rpc.listWorkspaces.start")
            async let wsFetch = client.listWorkspaces()
            var workspaces: [Workspace]
            do {
                workspaces = try await wsFetch
                StartupTrace.checkpoint("load.rpc.listWorkspaces.ok", "count=\(workspaces.count)")
            } catch {
                let nsErr = error as NSError
                print("[AppState.load] listWorkspaces FAILED domain=\(nsErr.domain) code=\(nsErr.code) desc=\(nsErr.localizedDescription)")
                throw error
            }
            StartupTrace.checkpoint("load.rpc.listProjects.start")
            let projects: [Project]
            do {
                projects = try await client.listProjects()
                StartupTrace.checkpoint("load.rpc.listProjects.ok", "count=\(projects.count)")
            } catch {
                let nsErr = error as NSError
                print("[AppState.load] listProjects FAILED domain=\(nsErr.domain) code=\(nsErr.code) desc=\(nsErr.localizedDescription)")
                throw error
            }

            // Phase 1 — connect the UI as soon as lists return (no per-workspace side effects yet).
            applyLoadedWorkspaces(workspaces, projects: projects)
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
                applyLoadedWorkspaces(workspaces, projects: projects)
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
                    let discovered = (try? await c.discoverPorts(workspaceID: ws.id)) ?? []
                    let spotlight = (try? await c.listPorts(workspaceId: ws.id)) ?? []
                    let merged = Self.mergeDiscoveredPortsWithSpotlight(discovered: discovered, spotlight: spotlight)
                    let status = (try? await c.tunnelStatus(workspaceId: ws.id))
                    return (ws.id, merged, status?.activeWorkspaceId ?? "")
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

    /// Compose/config discovery (`workspace.discover-ports`) plus active Spotlight forwards (`workspace.ports.list`).
    private nonisolated static func mergeDiscoveredPortsWithSpotlight(
        discovered: [[String: Any]],
        spotlight: [ForwardedPort]
    ) -> [ForwardedPort] {
        var byLocal: [Int: ForwardedPort] = [:]
        for d in discovered {
            let lp = (d["localPort"] as? Int) ?? (d["localPort"] as? NSNumber)?.intValue ?? 0
            guard lp > 0 else { continue }
            let rp = (d["remotePort"] as? Int) ?? (d["remotePort"] as? NSNumber)?.intValue ?? lp
            let svc = d["service"] as? String ?? ""
            let src = d["source"] as? String ?? ""
            let label: String
            if !svc.isEmpty, !src.isEmpty { label = "\(src): \(svc)" }
            else if !svc.isEmpty { label = svc }
            else if !src.isEmpty { label = src }
            else { label = "discovered" }
            byLocal[lp] = ForwardedPort(
                id: lp,
                remotePort: rp,
                preferred: false,
                tunneled: false,
                process: label,
                forwardId: nil
            )
        }
        for s in spotlight {
            // Spotlight (active tunnel) entries carry tunneling metadata but no process label.
            // Preserve the process name from the discovered entry so the column stays populated.
            let process = s.process ?? byLocal[s.port]?.process
            byLocal[s.port] = ForwardedPort(
                id: s.id,
                remotePort: s.remotePort,
                preferred: s.preferred,
                tunneled: s.tunneled,
                process: process,
                forwardId: s.forwardId
            )
        }
        return byLocal.keys.sorted().compactMap { byLocal[$0] }
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
    private func refreshDaemonStatus() async {
        guard let wsClient = client as? WebSocketDaemonClient else { return }
        if let info = await wsClient.fetchDaemonInfo() {
            daemonStatus = info.isCompatible ? .running(info: info) : .outdated(running: info)
            StartupTrace.checkpoint("daemon.status.ok", "v=\(info.version) protocol=\(info.protocolVersion)")
        } else {
            let synthetic = DaemonInfo(name: "nexus", version: "unknown", commit: "", builtAt: "", protocolVersion: DaemonInfo.requiredProtocol)
            daemonStatus = .running(info: synthetic)
            StartupTrace.checkpoint("daemon.status.no_http_version", "using placeholder info")
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
        guard let sshTarget = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines), !sshTarget.isEmpty else {
            connectionState = .disconnected
            error = "SSH target is required. This app only connects to a remote Nexus daemon over SSH."
            StartupTrace.checkpoint("remote.noSshTarget")
            return
        }
        connectionState = .connecting
        StartupTrace.checkpoint("remote.tunnel.start", "sshTarget=\(sshTarget) port=\(profile.port)")
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
            self.daemonLogStream?.stop()
            self.daemonLogStream = nil
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
            if let wsClient = self.client as? WebSocketDaemonClient {
                let stream = DaemonLogStream(client: wsClient)
                self.daemonLogStream = stream
                stream.start()
                Self.logger.info("DaemonLogStream started")
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
        daemonLogStream?.stop()
        daemonLogStream = nil
        await tunnelManager?.stop()
        tunnelManager = nil
        let profile = DaemonProfileStore().defaultProfile()
        self.cachedProfile = profile
        connectionState = .starting
        await connectRemoteAndLoad()
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
            if projects.isEmpty { await load() }
            guard let project = projects.first(where: { $0.id == request.projectId }) else {
                self.error = "Project not found."
                return
            }
            let ws: Workspace
            if request.fresh {
                let remote = try workspaceRepositoryPath(for: project)
                ws = try await client.createWorkspaceDaemon(spec: WorkspaceDaemonCreateSpec(
                    repo: remote,
                    ref: request.targetBranch,
                    workspaceName: request.workspaceName,
                    agentProfile: request.agentProfile,
                    backend: request.backend,
                    projectId: project.id
                ))
            } else {
                guard let parentId = request.sourceWorkspaceId, !parentId.isEmpty else {
                    self.error = "Choose a fork source or use Fresh."
                    return
                }
                ws = try await client.forkWorkspace(
                    parentID: parentId,
                    childName: request.workspaceName,
                    childRef: request.targetBranch
                )
            }
            await load()
            selectedWorkspaceID = ws.id
        } catch {
            self.error = error.localizedDescription
        }
    }

    public func ensureProjectRootSandbox(projectID: String) async -> Workspace? {
        if let existing = projectRootSandbox(projectID: projectID) {
            return existing
        }
        if projects.isEmpty || !projects.contains(where: { $0.id == projectID }) {
            await load()
            if let existing = projectRootSandbox(projectID: projectID) {
                return existing
            }
        }
        guard let project = projects.first(where: { $0.id == projectID }) else {
            self.error = "Project not found: \(projectID)"
            Self.logger.warning("ensureProjectRootSandbox: project not in list id=\(projectID, privacy: .public)")
            return nil
        }
        do {
            let remote = try workspaceRepositoryPath(for: project)
            let ws = try await client.createWorkspaceDaemon(spec: WorkspaceDaemonCreateSpec(
                repo: remote,
                ref: "main",
                workspaceName: project.name,
                agentProfile: "default",
                backend: "",
                projectId: project.id
            ))
            try LocalWorkspaceState.saveRecord(
                LocalWorkspaceRecord(
                    workspaceID: ws.id,
                    workspaceName: ws.workspaceName,
                    localPath: project.primaryRepo,
                    gitRoot: project.primaryRepo,
                    isWorktree: false
                )
            )
            await load()
            if let root = projectRootSandbox(projectID: projectID) {
                return root
            }
            self.error = "Project root sandbox creation did not appear in list"
            Self.logger.error("ensureProjectRootSandbox: workspace list missing new root for project=\(projectID, privacy: .public)")
            return nil
        } catch {
            await load()
            if let root = projectRootSandbox(projectID: projectID) {
                return root
            }
            self.error = error.localizedDescription
            Self.logger.error("ensureProjectRootSandbox failed: \(error.localizedDescription, privacy: .public)")
            return nil
        }
    }

    /// Returns the repo path to pass to `workspace.create` on the daemon.
    /// The path must already be an absolute path on the engine host.
    private func workspaceRepositoryPath(for project: Project) throws -> String {
        let raw = project.primaryRepo.trimmingCharacters(in: .whitespacesAndNewlines)
        guard raw.hasPrefix("/") else {
            throw NSError(
                domain: "NexusApp",
                code: 2,
                userInfo: [NSLocalizedDescriptionKey: "Repository path must be an absolute path on the daemon host (e.g. /home/you/project)."]
            )
        }
        return raw
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
        await perform { try await self.client.removeWorkspace(id: workspace.id) }
    }

    /// Removes a daemon project record, clears local mirror/bind state, and refreshes lists.
    public func removeProject(id: String) async {
        let wsIds = Set(repos.first(where: { $0.id == id })?.workspaces.map(\.id) ?? [])
        if let sel = selectedWorkspaceID, wsIds.contains(sel) {
            selectedWorkspaceID = nil
        }
        if let profile = cachedProfile {
            ProjectMirrorSync.shared.stopMirror(projectId: id, profile: profile)
        }
        ProjectRepoBindingStore.setUsesEnginePath(id, false)
        await perform { try await self.client.removeProject(id: id) }
    }

    public func addPort(_ port: Int, workspace: Workspace) async {
        await perform {
            try await self.client.addPortForward(workspaceId: workspace.id, localPort: port, remotePort: port)
        }
    }

    public func removePort(_ port: Int, workspace: Workspace) async {
        await perform {
            let fwdId = workspace.ports.first(where: { $0.port == port })?.forwardId
            guard let fwdId, !fwdId.isEmpty else {
                throw RPCError(message: "No spotlight forward id for this port — refresh and try again.")
            }
            try await self.client.removePortForward(workspaceId: workspace.id, forwardId: fwdId)
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
