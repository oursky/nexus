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
    @Published public var connectionState: ConnectionState = .disconnected {
        didSet {
            if connectionState == .disconnected {
                TerminalRegistry.shared.reset()
            }
        }
    }
    @Published public var daemonStatus: DaemonStatus = .unknown
    @Published public var showNewWorkspace = false
    @Published public var createIntent: CreateIntent?
    @Published public var sidebarVisible = true
    @Published public var showInspector = true
    @Published public var error: String?
    /// Human-readable progress message during auto-provisioning (nil when not provisioning).
    @Published public var provisioningMessage: String?
    /// Per-workspace in-flight operation state (start / stop / remove).
    /// Views observe this to show spinners and disable action buttons.
    @Published public var workspaceOps: [String: WorkspaceOpState] = [:]
    /// Live profile for the most recently created/forked workspace.
    @Published public var workspaceCreateProgress: WorkspaceCreateProgress?
    /// Per-workspace sync sessions (key = workspaceID).
    @Published public var syncSessions: [String: [SyncSession]] = [:]
    /// In-flight sync operation state.
    @Published public var syncOps: [String: SyncOpState] = [:]

    // MARK: - Client
    public private(set) var client: any DaemonClient

    /// Daemon profile used for the current remote connection (SSH target, tunnel port, etc.).
    public var activeDaemonProfile: DaemonProfile? { cachedProfile }

    /// Active daemon WebSocket URL (e.g. `ws://127.0.0.1:<port>`).
    /// Set after the SSH tunnel is established; nil before connecting.
    private var daemonWebSocketURL: URL?
    /// Bearer token for the active daemon connection.
    private var daemonToken: String?

    /// Reverse SSH tunnel port (set once after tunnel established).
    private var reverseTunnelPort: Int = 0

    private var refreshTask: Task<Void, Never>?
    private var tunnelStateTask: Task<Void, Never>?
    private var isLoadInProgress = false
    private var cachedProfile: DaemonProfile?
    private var tunnelManager: SSHTunnelManager?
    private var daemonLogStream: DaemonLogStream?
    private var workspaceCreateMonitorTasks: [String: Task<Void, Never>] = [:]
    /// PIDs of long-lived `nexus spotlight run <workspaceID>` processes.
    /// Key = workspaceID. SIGTERM on stop.
    private var spotlightCLIProcesses: [String: Int32] = [:]
    private lazy var spotlightManager = SpotlightManager(client: self.client)

    /// Set when the last connection attempt failed for a configuration reason that
    /// cannot be resolved by retrying (e.g. missing SSH identity key on a sandboxed build).
    /// The auto-reconnect loop skips retries while this is true.  Cleared on reconnect().
    @Published public private(set) var needsSetup: Bool = false

    /// Headless HTTP RPC server for terminal automation (active when NEXUS_HEADLESS_RPC=1).
    public private(set) var rpcServer: HeadlessRPCServer?


    public init() {
        AppLifecycleLog.configure()
        AppLifecycleLog.info("app", "AppState init start")
        StartupTrace.beginSession()
        StartupTrace.checkpoint("app.init", "before client")

        let profile = DaemonProfileStore().defaultProfile()
        if let profile {
            AppLifecycleLog.info("app", "default profile loaded id=\(profile.profileId) hasTarget=\(profile.sshTarget != nil)")
        } else {
            AppLifecycleLog.warn("app", "no default profile configured")
        }
        self.cachedProfile = profile
        self.client = NullDaemonClient()
        connectionState = .starting
        StartupTrace.checkpoint("app.init", "after client; scheduling load")
        Task { await self.connectRemoteAndLoad() }
        startRefreshLoop()
        AppLifecycleLog.info("rpc", "startRPCServer begin")
        StartupTrace.checkpoint("app.init", "before startRPCServer")
        startRPCServer()
        AppLifecycleLog.info("rpc", "startRPCServer end")
        StartupTrace.checkpoint("app.init", "after startRPCServer")
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

        // Project-linked workspaces appear under their project group.
        // Unlinked workspaces (created via CLI, no projectId) fall back to
        // path-based grouping and are appended so they always stay visible.
        let projectRepos = Repo.fromProjects(projects, workspaces: carried)
        let projectWorkspaceIDs = Set(projectRepos.flatMap { $0.workspaces.map(\.id) })
        let unlinked = carried.filter { !projectWorkspaceIDs.contains($0.id) }
        repos = projectRepos + Repo.grouping(unlinked)
        connectionState = .connected
        error = nil

        if selectedWorkspaceID == nil {
            selectedWorkspaceID = repos.first?.workspaces.first?.id
        }
    }

    // MARK: - Load

    /// Wall-clock cap for markWorkspaceReady / ports / tunnel fan-out (many actives can wedge the daemon).
    private static let workspaceEnrichmentDeadlineSeconds: UInt64 = 35
    /// Upper bound on active workspaces to enrich per load cycle.
    private static let workspaceEnrichmentMaxWorkspaces = 8
    /// Hard cap for the entire auto-start + first successful data fetch (independent of per-RPC timeouts).
    private static let startupDeadlineSeconds: UInt64 = 120

    public func load() async {
        if isLoadInProgress {
            StartupTrace.checkpoint("load.skip.inflight")
            Self.logger.debug("load() skipped: already in progress")
            return
        }
        isLoadInProgress = true
        defer { isLoadInProgress = false }

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

            // Pre-populate PTY session managers for active workspaces so
            // existing terminal tabs are discoverable immediately after reconnect
            // (reload-window behavior). Capped at 8 to match the Phase 2
            // enrichment limit and prevent resource saturation from polling loops.
            if let wsClient = client as? WebSocketDaemonClient {
                let active = workspaces.filter { $0.state.isActive }
                let capped = active.prefix(8)
                for ws in capped {
                    TerminalRegistry.shared.ensureManager(workspaceId: ws.id, client: wsClient)
                }
            }

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
        let active = workspaces.filter { $0.state.isActive }
        if active.isEmpty {
            return workspaces
        }

        let selectedID = selectedWorkspaceID
        let prioritized = active.sorted { lhs, rhs in
            let l = (lhs.id == selectedID)
            let r = (rhs.id == selectedID)
            if l != r { return l && !r }
            return lhs.id < rhs.id
        }
        let target = Array(prioritized.prefix(Self.workspaceEnrichmentMaxWorkspaces))
        if active.count > target.count {
            StartupTrace.checkpoint("load.phase2.trimmed", "active=\(active.count) cap=\(Self.workspaceEnrichmentMaxWorkspaces)")
            Self.logger.notice("load() enrichment trimmed: active=\(active.count, privacy: .public) cap=\(Self.workspaceEnrichmentMaxWorkspaces, privacy: .public)")
        }

        await withTaskGroup(of: (String, [ForwardedPort], String).self) { group in
            for ws in target {
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
        if !activeTunnelWorkspaceID.isEmpty {
            for i in workspaces.indices {
                workspaces[i].hasActiveTunnels = (workspaces[i].id == activeTunnelWorkspaceID)
            }
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
            if !svc.isEmpty, src == "compose" { label = svc }
            else if !svc.isEmpty, !src.isEmpty { label = "\(src): \(svc)" }
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

    private func startRPCServer() {
        AppLifecycleLog.info("rpc", "startRPCServer constructing HeadlessRPCServer")
        let server = HeadlessRPCServer(clientProvider: { [weak self] in
            self?.client as? WebSocketDaemonClient
        })
        server.openEditorAction = { [weak self] workspaceID, app, checkOnly in
            guard let self else { return (false, "AppState deallocated") }
            return await self.openEditorViaCLI(workspaceID: workspaceID, app: app, checkOnly: checkOnly)
        }
        server.spotlightStartAction = { [weak self] workspaceID in
            guard let self else { return (false, "AppState deallocated") }
            do {
                self.killSpotlightCLI(workspaceID: workspaceID)
                let output = try await self.runSpotlightRun(workspaceID: workspaceID)
                return (true, output)
            } catch {
                return (false, error.localizedDescription)
            }
        }
        server.spotlightStopAction = { [weak self] workspaceID in
            guard let self else { return (false, "AppState deallocated") }
            self.killSpotlightCLI(workspaceID: workspaceID)
            return (true, "spotlight stopped")
        }
        server.daemonCheckAction = { [weak self] driver in
            guard let self else { return (false, "AppState deallocated") }
            return await self.runDaemonCheckViaCLI(driver: driver)
        }
        // Wire workspace lifecycle + provisioning providers for headless RPC.
        server.daemonClientProvider = { [weak self] in
            guard let self else { return nil }
            guard case .connected = self.connectionState else { return nil }
            guard !(self.client is NullDaemonClient) else { return nil }
            return self.client
        }
        server.daemonProfileProvider = { [weak self] in
            self?.activeDaemonProfile
        }
        server.daemonProfilesProvider = {
            DaemonProfileStore().load()
        }
        server.daemonProfilesSaveAction = { [weak self] profile in
            guard let self else { return (false, "AppState deallocated") }
            let valid = self.validateProfile(profile)
            guard valid.0 else { return valid }

            let store = DaemonProfileStore()
            var profiles = store.load()
            if profile.isDefault {
                profiles = profiles.map { p in
                    var c = p
                    c.isDefault = false
                    return c
                }
            }
            profiles.append(profile)
            store.save(profiles)
            AppLifecycleLog.info("rpc", "saved profile via headless rpc id=\(profile.profileId)")
            await self.reconnect()
            return (true, "saved and reconnected")
        }
        server.daemonReconnectAction = { [weak self] in
            guard let self else { return (false, "AppState deallocated") }
            await self.reconnect()
            AppLifecycleLog.info("rpc", "reconnect requested via headless rpc")
            return (true, "reconnected")
        }
        server.daemonDisconnectAction = { [weak self] in
            guard let self else { return (false, "AppState deallocated") }
            await self.tunnelManager?.stop()
            self.tunnelManager = nil
            self.connectionState = .disconnected
            self.error = nil
            AppLifecycleLog.info("rpc", "disconnected via headless rpc")
            return (true, "disconnected")
        }
        server.daemonProfileDeleteAction = { [weak self] profileId in
            guard let self else { return (false, "AppState deallocated") }
            let store = DaemonProfileStore()
            var profiles = store.load()
            let before = profiles.count
            profiles.removeAll { $0.profileId == profileId }
            guard profiles.count < before else {
                return (false, "profile not found: \(profileId)")
            }
            store.save(profiles)
            // If we deleted the active profile, disconnect.
            if self.cachedProfile?.profileId == profileId {
                await self.tunnelManager?.stop()
                self.tunnelManager = nil
                self.connectionState = .disconnected
                self.cachedProfile = nil
            }
            AppLifecycleLog.info("rpc", "deleted profile via headless rpc id=\(profileId)")
            return (true, "deleted")
        }
        server.daemonProfileSetActiveAction = { [weak self] profileId in
            guard let self else { return (false, "AppState deallocated") }
            let store = DaemonProfileStore()
            var profiles = store.load()
            guard let idx = profiles.firstIndex(where: { $0.profileId == profileId }) else {
                return (false, "profile not found: \(profileId)")
            }
            // Clear all defaults, set the target.
            for i in profiles.indices { profiles[i].isDefault = false }
            profiles[idx].isDefault = true
            store.save(profiles)
            AppLifecycleLog.info("rpc", "set active profile via headless rpc id=\(profileId)")
            await self.reconnect()
            return (true, "set active and reconnected")
        }
        server.appLogTailProvider = { lines in
            Self.tailFile(path: Self.appLifecycleLogPath(), lines: lines)
        }
        server.connectionSnapshotProvider = { [weak self] in
            guard let self else { return ["available": false] }
            let stateText: String
            switch self.connectionState {
            case .starting: stateText = "starting"
            case .disconnected: stateText = "disconnected"
            case .connecting: stateText = "connecting"
            case .connected: stateText = "connected"
            case .provisioning(let step): stateText = "provisioning:\(step)"
            }
            let daemonText: String
            switch self.daemonStatus {
            case .unknown: daemonText = "unknown"
            case .running: daemonText = "running"
            case .outdated: daemonText = "outdated"
            case .offline: daemonText = "offline"
            }
            return [
                "available": true,
                "connectionState": stateText,
                "daemonStatus": daemonText,
                "clientType": String(describing: type(of: self.client)),
                "hasProfile": self.activeDaemonProfile != nil,
                "daemonWebSocketURL": self.daemonWebSocketURL?.absoluteString as Any,
                "daemonToken": self.daemonToken != nil ? "set(\(self.daemonToken!.count)chars)" : "nil",
                "sshTarget": self.cachedProfile?.sshTarget as Any,
                "reverseTunnelPort": self.reverseTunnelPort,
            ]
        }
        rpcServer = server
        AppLifecycleLog.info("rpc", "headless server configured; calling start()")
        server.start()
        AppLifecycleLog.info("rpc", "headless server start() returned")
    }

    private func validateProfile(_ profile: DaemonProfile) -> (Bool, String) {
        let name = profile.name.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty { return (false, "Profile name is required") }
        let target = (profile.sshTarget ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if target.isEmpty { return (false, "SSH target is required") }
        if !target.contains("@") || target.hasPrefix("@") || target.hasSuffix("@") {
            return (false, "SSH target must be in form user@host")
        }
        if let p = profile.sshPort, !(1...65535).contains(p) {
            return (false, "sshPort must be 1..65535")
        }
        if !(1...65535).contains(profile.port) {
            return (false, "port must be 1..65535")
        }
        return (true, "ok")
    }

    private static func appLifecycleLogPath() -> String {
        let configHome = ProcessInfo.processInfo.environment["XDG_CONFIG_HOME"]
            ?? "\(ProcessInfo.processInfo.environment["HOME"] ?? NSHomeDirectory())/.config"
        return "\(configHome)/nexus/run/nexusapp.log"
    }

    private static func tailFile(path: String, lines: Int) -> String {
        guard let content = try? String(contentsOfFile: path, encoding: .utf8) else {
            return ""
        }
        let all = content.split(separator: "\n", omittingEmptySubsequences: false)
        let tail = all.suffix(max(0, lines))
        return tail.joined(separator: "\n")
    }

    // MARK: - Editor open via CLI

    private static let openEditorLogger = Logger(subsystem: "com.nexus.NexusApp", category: "OpenEditor")

    /// Spawns `nexus workspace open-editor <id> [--check]` and returns (ok, combinedOutput).
    /// Passes the app's current daemon WebSocket URL + token so the subprocess reuses
    /// the existing SSH tunnel without needing its own profile or SSH agent.
    public func openEditorViaCLI(workspaceID: String, app: String, checkOnly: Bool) async -> (Bool, String) {
        let log = Self.openEditorLogger
        let wsURL = daemonWebSocketURL?.absoluteString
        let tok = daemonToken
        let sshHost = cachedProfile?.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines)
        let sshPort = cachedProfile?.sshPort
        let sshIdentity = cachedProfile?.sshIdentity?.trimmingCharacters(in: .whitespacesAndNewlines)

        if let sshHost, !sshHost.isEmpty,
           let workspace = repos.flatMap(\.workspaces).first(where: { $0.id == workspaceID }),
           let spec = workspace.remoteSSHFolderOpen(jumpHost: sshHost, identityFile: sshIdentity),
           let guestIP = spec.vmGuestIP,
           !guestIP.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            do {
                try NexusSSHConfigSnippet.installIncludeIfNeeded()
                try NexusSSHConfigSnippet.writeVMJumpHost(
                    hostAlias: spec.sshHostForURI,
                    guestIP: guestIP,
                    proxyJump: spec.proxyJump,
                    identityFile: spec.identityFile
                )
                log.info("open-editor prewrote SSH alias \(spec.sshHostForURI, privacy: .public)")
            } catch {
                // Sandboxed app builds may not have direct access to the real
                // ~/.ssh config. Fall back to the CLI path, which still writes
                // its own alias snippet before opening the editor.
                log.error("open-editor prewrite skipped: \(error.localizedDescription, privacy: .public)")
            }
        }

        log.info("open-editor start workspaceID=\(workspaceID, privacy: .public) app=\(app, privacy: .public) checkOnly=\(checkOnly, privacy: .public)")
        log.debug("open-editor daemonURL=\(wsURL ?? "(nil)", privacy: .public) hasToken=\(tok != nil, privacy: .public)")

        return await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let nexusBin = Self.nexusBinaryPath()
                log.info("open-editor binary=\(nexusBin, privacy: .public)")

                var env = ProcessInfo.processInfo.environment
                env["SHELL"] = "/bin/sh"
                if let u = wsURL    { env["NEXUS_E2E_DAEMON_WEBSOCKET"] = u }
                if let t = tok      { env["NEXUS_DAEMON_TOKEN"] = t }
                if let h = sshHost, !h.isEmpty { env["NEXUS_DAEMON_SSH_HOST"] = h }
                if let p = sshPort, p > 0 { env["NEXUS_DAEMON_SSH_PORT"] = "\(p)" }
                if let id = sshIdentity, !id.isEmpty {
                    env["NEXUS_DAEMON_SSH_IDENTITY"] = id
                }

                var args = ["workspace", "open-editor", workspaceID, "--app", app]
                if checkOnly { args.append("--check") }
                log.info("open-editor running: \(nexusBin) \(args.joined(separator: " "), privacy: .public)")

                let proc = Process()
                proc.executableURL = URL(fileURLWithPath: nexusBin)
                proc.arguments = args
                proc.environment = env

                let outPipe = Pipe()
                let errPipe = Pipe()
                proc.standardOutput = outPipe
                proc.standardError  = errPipe

                do {
                    try proc.run()
                } catch {
                    log.error("open-editor launch failed: \(error.localizedDescription, privacy: .public)")
                    continuation.resume(returning: (false, "Could not launch nexus: \(error.localizedDescription)"))
                    return
                }
                proc.waitUntilExit()

                let out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let combined = [out, err].filter { !$0.isEmpty }.joined(separator: "\n")
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                let ok = proc.terminationStatus == 0

                if ok {
                    log.info("open-editor succeeded workspaceID=\(workspaceID, privacy: .public)")
                    continuation.resume(returning: (true, combined))
                } else {
                    log.error("open-editor failed (exit \(proc.terminationStatus, privacy: .public)): \(combined, privacy: .public)")
                    continuation.resume(returning: (false, combined))
                }
            }
        }
    }

    // MARK: - Workspace export / import via CLI
    #if FEATURE_EXPORT_IMPORT

    private static let bundleLogger = Logger(subsystem: "com.nexus.NexusApp", category: "Bundle")

    /// Spawns `nexus workspace export <id> --out <path>` and returns (ok, combinedOutput).
    public func exportWorkspaceViaCLI(workspaceID: String, outPath: String) async -> (Bool, String) {
        let log = Self.bundleLogger
        let wsURL = daemonWebSocketURL?.absoluteString
        let tok = daemonToken
        let sshHost = cachedProfile?.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines)
        let sshPort = cachedProfile?.sshPort
        let sshIdentity = cachedProfile?.sshIdentity?.trimmingCharacters(in: .whitespacesAndNewlines)

        log.info("export start workspaceID=\(workspaceID, privacy: .public) out=\(outPath, privacy: .public)")

        return await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let nexusBin = Self.nexusBinaryPath()

                var env = ProcessInfo.processInfo.environment
                env["SHELL"] = "/bin/sh"
                if let u = wsURL    { env["NEXUS_E2E_DAEMON_WEBSOCKET"] = u }
                if let t = tok      { env["NEXUS_DAEMON_TOKEN"] = t }
                if let h = sshHost, !h.isEmpty { env["NEXUS_DAEMON_SSH_HOST"] = h }
                if let p = sshPort, p > 0 { env["NEXUS_DAEMON_SSH_PORT"] = "\(p)" }
                if let id = sshIdentity, !id.isEmpty {
                    env["NEXUS_DAEMON_SSH_IDENTITY"] = id
                }

                let args = ["workspace", "export", workspaceID, "--out", outPath]
                log.info("export running: \(nexusBin) \(args.joined(separator: " "), privacy: .public)")

                let proc = Process()
                proc.executableURL = URL(fileURLWithPath: nexusBin)
                proc.arguments = args
                proc.environment = env

                let outPipe = Pipe()
                let errPipe = Pipe()
                proc.standardOutput = outPipe
                proc.standardError  = errPipe

                do {
                    try proc.run()
                } catch {
                    log.error("export launch failed: \(error.localizedDescription, privacy: .public)")
                    continuation.resume(returning: (false, "Could not launch nexus: \(error.localizedDescription)"))
                    return
                }
                proc.waitUntilExit()

                let out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let combined = [out, err].filter { !$0.isEmpty }.joined(separator: "\n")
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                let ok = proc.terminationStatus == 0

                if ok {
                    log.info("export succeeded workspaceID=\(workspaceID, privacy: .public)")
                    continuation.resume(returning: (true, combined))
                } else {
                    log.error("export failed (exit \(proc.terminationStatus, privacy: .public)): \(combined, privacy: .public)")
                    continuation.resume(returning: (false, combined))
                }
            }
        }
    }

    /// Spawns `nexus workspace import --from <path>` and returns (ok, combinedOutput).
    public func importWorkspaceViaCLI(fromPath: String) async -> (Bool, String) {
        let log = Self.bundleLogger
        let wsURL = daemonWebSocketURL?.absoluteString
        let tok = daemonToken
        let sshHost = cachedProfile?.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines)
        let sshPort = cachedProfile?.sshPort
        let sshIdentity = cachedProfile?.sshIdentity?.trimmingCharacters(in: .whitespacesAndNewlines)

        log.info("import start from=\(fromPath, privacy: .public)")

        return await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let nexusBin = Self.nexusBinaryPath()

                var env = ProcessInfo.processInfo.environment
                env["SHELL"] = "/bin/sh"
                if let u = wsURL    { env["NEXUS_E2E_DAEMON_WEBSOCKET"] = u }
                if let t = tok      { env["NEXUS_DAEMON_TOKEN"] = t }
                if let h = sshHost, !h.isEmpty { env["NEXUS_DAEMON_SSH_HOST"] = h }
                if let p = sshPort, p > 0 { env["NEXUS_DAEMON_SSH_PORT"] = "\(p)" }
                if let id = sshIdentity, !id.isEmpty {
                    env["NEXUS_DAEMON_SSH_IDENTITY"] = id
                }

                let args = ["workspace", "import", "--from", fromPath]
                log.info("import running: \(nexusBin) \(args.joined(separator: " "), privacy: .public)")

                let proc = Process()
                proc.executableURL = URL(fileURLWithPath: nexusBin)
                proc.arguments = args
                proc.environment = env

                let outPipe = Pipe()
                let errPipe = Pipe()
                proc.standardOutput = outPipe
                proc.standardError  = errPipe

                do {
                    try proc.run()
                } catch {
                    log.error("import launch failed: \(error.localizedDescription, privacy: .public)")
                    continuation.resume(returning: (false, "Could not launch nexus: \(error.localizedDescription)"))
                    return
                }
                proc.waitUntilExit()

                let out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let combined = [out, err].filter { !$0.isEmpty }.joined(separator: "\n")
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                let ok = proc.terminationStatus == 0

                if ok {
                    log.info("import succeeded")
                    continuation.resume(returning: (true, combined))
                } else {
                    log.error("import failed (exit \(proc.terminationStatus, privacy: .public)): \(combined, privacy: .public)")
                    continuation.resume(returning: (false, combined))
                }
            }
        }
    }
    #endif

    // MARK: - Daemon health check (runs on daemon host via SSH)

    private static let daemonCheckLogger = Logger(subsystem: "com.nexus.NexusApp", category: "DaemonCheck")

    /// Runs `nexus daemon check [--driver <driver>]` **on the daemon host** via SSH and
    /// returns (allPassed, combinedOutput).
    ///
    /// The checks verify the daemon host's environment (KVM access, kernel image, rootfs,
    /// guest agent, SSH keys, auth tokens, etc.) so they must run on the remote machine,
    /// not locally on the Mac.
    public func runDaemonCheckViaCLI(driver: String? = nil) async -> (Bool, String) {
        let log = Self.daemonCheckLogger
        log.info("daemon check start driver=\(driver ?? "(auto)", privacy: .public)")

        guard let profile = cachedProfile,
              let sshTarget = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines),
              !sshTarget.isEmpty else {
            return (false, "No daemon profile configured — connect to a daemon host first.")
        }

        return await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                // Build the remote nexus command.
                var remoteCmd = "~/.local/bin/nexus daemon check"
                if let d = driver, !d.isEmpty { remoteCmd += " --driver \(d)" }
                log.info("daemon check ssh target=\(sshTarget, privacy: .public) cmd=\(remoteCmd, privacy: .public)")

                // Build ssh arguments via shared builder (strict-key enforcement included).
                let client = SSHClientArgs(
                    sshTarget: sshTarget,
                    port: profile.sshPort,
                    identityPath: profile.sshIdentity,
                    configPath: nil
                )
                let sshArgs = client.commandArgs(remoteCommand: [remoteCmd])

                let proc = Process()
                proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
                proc.arguments = sshArgs

                let outPipe = Pipe()
                let errPipe = Pipe()
                proc.standardOutput = outPipe
                proc.standardError  = errPipe

                var env = ProcessInfo.processInfo.environment
                env["SHELL"] = "/bin/sh"
                proc.environment = env

                do {
                    try proc.run()
                } catch {
                    log.error("daemon check ssh failed: \(error.localizedDescription, privacy: .public)")
                    continuation.resume(returning: (false, "SSH launch failed: \(error.localizedDescription)"))
                    return
                }
                proc.waitUntilExit()

                let out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                let combined = [out, err].filter { !$0.isEmpty }.joined(separator: "\n")
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                let ok = proc.terminationStatus == 0
                log.info("daemon check finished ok=\(ok, privacy: .public)")
                continuation.resume(returning: (ok, combined))
            }
        }
    }

    private static func nexusBinaryPath() -> String {
        // Prefer the binary bundled inside the app bundle.
        if let url = Bundle.main.url(forResource: "nexus", withExtension: nil) {
            return url.path
        }
        // Development fallback when running without a full app bundle.
        let devPaths = [
            "/Users/\(NSUserName())/.local/bin/nexus",
            "/usr/local/bin/nexus",
        ]
        return devPaths.first { FileManager.default.isExecutableFile(atPath: $0) } ?? devPaths[0]
    }

    private func startTunnelStateObserver(_ mgr: SSHTunnelManager) {
        tunnelStateTask?.cancel()
        tunnelStateTask = Task { [weak self] in
            for await state in await mgr.stateStream {
                guard !Task.isCancelled, let self else { break }
                if case .failed(let err) = state {
                    Self.logger.warning("tunnelStateObserver: tunnel failed — \(err.localizedDescription, privacy: .public)")
                    self.connectionState = .disconnected
                    if self.error == nil {
                        self.error = "SSH tunnel failed: \(err.localizedDescription)"
                    }
                    // Do not set needsSetup here — the restart loop inside SSHTunnelManager
                    // will attempt to recover. needsSetup is only set when start() itself
                    // fails (i.e. initial connection rejected), which is handled in connectRemoteAndLoad.
                }
            }
        }
    }

    private func startRefreshLoop() {        refreshTask?.cancel()
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(4))
                guard !Task.isCancelled, let self else { break }
                // Never poll `load()` during initial handshake — it stacks concurrent RPCs,
                // duplicates WebSocket work, and grows memory while the UI stays on "Starting…".
                if self.connectionState == .starting || self.connectionState == .connecting {
                    continue
                }
                // Auto-heal transient tunnel/bootstrap failures. If we are disconnected
                // and still on a null client, re-run full remote connect flow instead
                // of repeatedly polling load() against a known-disconnected client.
                // Skip retries when the failure is a configuration error that the user
                // must fix manually (e.g. missing SSH identity key in the sandboxed app).
                if self.connectionState == .disconnected,
                   self.client is NullDaemonClient,
                   self.tunnelManager == nil,
                   !self.needsSetup {
                    StartupTrace.checkpoint("remote.autoReconnect.tick")
                    await self.connectRemoteAndLoad()
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
        AppLifecycleLog.info("remote", "connectRemoteAndLoad enter cachedProfile=\(cachedProfile != nil)")
        guard let profile = cachedProfile else {
            connectionState = .disconnected
            error = "No remote profile configured. Add one in Settings."
            needsSetup = true
            StartupTrace.checkpoint("remote.noProfile")
            AppLifecycleLog.warn("remote", "connect aborted: no profile")
            return
        }
        guard let sshTarget = profile.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines), !sshTarget.isEmpty else {
            connectionState = .disconnected
            error = "SSH target is required. This app only connects to a remote Nexus daemon over SSH."
            needsSetup = true
            StartupTrace.checkpoint("remote.noSshTarget")
            AppLifecycleLog.warn("remote", "connect aborted: empty ssh target profileId=\(profile.profileId)")
            return
        }
        guard Self.isLikelyValidSSHTarget(sshTarget) else {
            connectionState = .disconnected
            error = "SSH target must be in the form user@host (no spaces)."
            needsSetup = true
            StartupTrace.checkpoint("remote.invalidSshTarget", sshTarget)
            AppLifecycleLog.warn("remote", "connect aborted: invalid ssh target=\(sshTarget)")
            return
        }
        connectionState = .connecting
        StartupTrace.checkpoint("remote.tunnel.start", "sshTarget=\(sshTarget) port=\(profile.port)")

        // ── Skip auto-provision on start; user must manually provision via Settings ──
        // Provisioning is available via the "Provision Daemon" button in Daemon Settings.
        // This avoids unexpected binary uploads / daemon restarts on every app launch.
        let mgr = SSHTunnelManager(profile: profile)
        self.tunnelManager = mgr
        startTunnelStateObserver(mgr)
        let daemonURL: URL
        let resolvedToken: String
        do {
            let localPort = try await mgr.start()
            reverseTunnelPort = await mgr.reversePort
            StartupTrace.checkpoint("remote.tunnel.ok", "localPort=\(localPort) reversePort=\(reverseTunnelPort)")
            resolvedToken = try await mgr.fetchRemoteToken()
            StartupTrace.checkpoint("remote.token.ok", "tokenLen=\(resolvedToken.count)")
            guard let url = URL(string: "ws://127.0.0.1:\(localPort)") else {
                connectionState = .disconnected
                error = "Tunnel started but could not form local URL"
                return
            }
            daemonURL = url
            daemonWebSocketURL = url
            daemonToken = resolvedToken.isEmpty ? nil : resolvedToken
        } catch {
            connectionState = .disconnected
            self.error = "SSH tunnel failed: \(error.localizedDescription)"
            StartupTrace.checkpoint("remote.tunnel.failed", error.localizedDescription)
            AppLifecycleLog.error("remote", "tunnel failed target=\(sshTarget): \(error.localizedDescription)")
            self.daemonLogStream?.stop()
            self.daemonLogStream = nil
            self.tunnelStateTask?.cancel()
            self.tunnelStateTask = nil
            self.tunnelManager = nil
            // Any tunnel failure requires user action (wrong key, bad host, daemon not running).
            // Stop the auto-reconnect loop entirely — hammering SSH with a bad key burns CPU
            // and produces no useful outcome. The user must fix the profile and trigger reconnect().
            needsSetup = true
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

                // Push handler: daemon broadcasts workspace.ref when a git checkout
                // occurs inside a VM. Patch the matching workspace in-place so the
                // sidebar and breadcrumb update immediately without a full reload.
                wsClient.setWorkspaceRefHandler { [weak self] workspaceID, ref in
                    Task { @MainActor [weak self] in
                        guard let self else { return }
                        self.repos = self.repos.map { repo in
                            var r = repo
                            r.workspaces = r.workspaces.map { ws in
                                guard ws.id == workspaceID else { return ws }
                                var updated = ws
                                updated.ref = ref
                                return updated
                            }
                            return r
                        }
                        Self.logger.debug("workspace.ref push: id=\(workspaceID) ref=\(ref)")
                    }
                }
            }
            self.updateProfileStatus(profileId: profile.profileId, status: .connected)

            // Register tunnel with daemon via daemon.connect RPC
            let rp = await mgr.reversePort
            if rp > 0, let wsClient = self.client as? WebSocketDaemonClient {
                Task {
                    let clientUser = NSUserName()
                    let clientPath = NSHomeDirectory()
                    let res = try? await wsClient.call("daemon.connect", params: [
                        "token": resolvedToken,
                        "reversePort": rp,
                        "clientUser": clientUser,
                        "clientPath": clientPath,
                    ] as [String: Any])
                    if let res = res, let dict = res as? [String: Any], dict["ok"] as? Bool == true {
                        Self.logger.info("daemon.connect ok, reversePort=\(rp)")
                    } else {
                        let errorMsg = "daemon.connect returned non-ok (reversePort=\(rp))"
                        Self.logger.error("\(errorMsg)")
                        await MainActor.run {
                            if self.error == nil {
                                self.error = errorMsg
                            }
                        }
                    }
                }
            }
        } catch {
            if connectionState != .connected {
                connectionState = .disconnected
                self.error = "Remote daemon unreachable: \(daemonURL.host ?? ""):\(daemonURL.port ?? 0) — \(error.localizedDescription)"
                StartupTrace.checkpoint("remote.connect.failed", error.localizedDescription)
                Self.logger.error("connectRemoteAndLoad failed: \(error.localizedDescription, privacy: .public)")
                AppLifecycleLog.error("remote", "connect failed daemonURL=\(daemonURL.absoluteString) error=\(error.localizedDescription)")
                self.updateProfileStatus(profileId: profile.profileId, status: .unreachable)
            }
        }
    }

    private static func isLikelyValidSSHTarget(_ value: String) -> Bool {
        let s = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return false }
        if s.contains(" ") || s.contains("\t") || s.contains("\n") { return false }
        guard s.contains("@"), !s.hasPrefix("@"), !s.hasSuffix("@") else { return false }
        return true
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


    /// Called on app termination to clean up SSH tunnels and WebSocket connections.
    public func shutdown() {
        // Stop all spotlight SSH tunnels and daemon-side spotlight
        spotlightManager.stopAll()
        // Stop the tunnel manager (kills all SSH processes)
        let tm = tunnelManager
        tunnelManager = nil
        if let tm = tm {
            let semaphore = DispatchSemaphore(value: 0)
            Task {
                await tm.stop()
                semaphore.signal()
            }
            _ = semaphore.wait(timeout: .now() + 5.0)
        }
        // Disconnect WebSocket client (best-effort, may already be gone)
        if let wsClient = client as? WebSocketDaemonClient {
            wsClient.disconnect()
        }
        client = NullDaemonClient()
    }

    /// Re-reads the default profile and reconnects (e.g. after the user changes the active profile).
    public func reconnect() async {
        // Stop all spotlight SSH tunnels and daemon-side spotlight before reconnecting
        spotlightManager.stopAll()
        // Clear any config error that was blocking the auto-reconnect loop.
        needsSetup = false
        daemonLogStream?.stop()
        daemonLogStream = nil
        await tunnelManager?.stop()
        tunnelManager = nil
        TerminalRegistry.shared.reset()
        let profile = DaemonProfileStore().defaultProfile()
        self.cachedProfile = profile
        connectionState = .starting
        await connectRemoteAndLoad()
    }

    /// Manually trigger remote daemon provisioning (upload binary + start daemon).
    /// Useful after changing the host profile or when the automatic provisioning at
    /// connect-time failed. The full connection is re-established afterward.
    public func installDaemon() {
        Task {
            guard let profile = cachedProfile, profile.sshTarget != nil else {
                error = "No SSH host configured. Open daemon settings to add one."
                return
            }
            connectionState = .provisioning(step: "Provisioning daemon…")
            provisioningMessage = "Provisioning daemon…"
            let provisioner = RemoteProvisioner(profile: profile)
            do {
                try await provisioner.provision { [weak self] step in
                    guard let self else { return }
                    let msg: String
                    switch step {
                    case .checkingHost:                msg = "Checking remote host…"
                    case .uploadingBinary(let pct):   msg = String(format: "Uploading Nexus (%.0f%%)…", pct * 100)
                    case .startingDaemon:             msg = "Starting daemon…"
                    case .bootstrapPhase(let phase, let m):
                        msg = Self.provisioningStatusMessage(phase: phase, message: m)
                    case .waitingForDaemon(let n):    msg = n <= 1 ? "Waiting for daemon…" : "Waiting (\(n))…"
                    case .ready:                      msg = "Daemon ready"
                    }
                    await MainActor.run { [weak self] in
                        self?.connectionState = .provisioning(step: msg)
                        self?.provisioningMessage = msg
                        AppLifecycleLog.info("provision", msg)
                    }
                }
                Self.logger.info("installDaemon: provisioning succeeded")
            } catch {
                Self.logger.error("installDaemon: \(error.localizedDescription, privacy: .public)")
                self.error = "Daemon installation failed: \(error.localizedDescription)"
            }
            provisioningMessage = nil
            // Re-connect after provisioning completes (success or failure).
            await reconnect()
        }
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
            AppLifecycleLog.info("workspace", "createSandbox begin projectID=\(request.projectId) fresh=\(request.fresh)")
            workspaceCreateProgress = nil
            if projects.isEmpty { await load() }
            guard let project = projects.first(where: { $0.id == request.projectId }) else {
                self.error = "Project not found."
                AppLifecycleLog.warn("workspace", "createSandbox rejected: project not found id=\(request.projectId)")
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
            workspaceOps[ws.id] = .starting(detail: "Preparing VM…")
            pollUntilStarted(workspaceID: ws.id, workspaceName: ws.workspaceName)
            await load()
            selectedWorkspaceID = ws.id
            AppLifecycleLog.info("workspace", "createSandbox success workspaceID=\(ws.id) projectID=\(project.id)")
        } catch {
            self.error = error.localizedDescription
            AppLifecycleLog.error("workspace", "createSandbox failed projectID=\(request.projectId): \(error.localizedDescription)")
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
        let trimmedRepo = repo.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            AppLifecycleLog.info("project", "createProject begin repo=\(trimmedRepo)")
            let project = try await client.createProject(repo: trimmedRepo)
            await load()
            guard let rootSandbox = await ensureProjectRootSandbox(projectID: project.id) else {
                AppLifecycleLog.warn("project", "createProject created but no root sandbox projectID=\(project.id)")
                return nil
            }
            selectedWorkspaceID = rootSandbox.id
            AppLifecycleLog.info("project", "createProject success projectID=\(project.id) rootWorkspaceID=\(rootSandbox.id)")
            return project
        } catch {
            let message = error.localizedDescription
            if Self.isAlreadyExistsError(message),
               let recovered = await recoverExistingProject(repo: trimmedRepo) {
                AppLifecycleLog.warn("project", "createProject upsert recovered existing projectID=\(recovered.id)")
                await load()
                if let rootSandbox = await ensureProjectRootSandbox(projectID: recovered.id) {
                    selectedWorkspaceID = rootSandbox.id
                } else {
                    AppLifecycleLog.warn("project", "upsert recovered project but root sandbox missing projectID=\(recovered.id)")
                }
                return recovered
            }
            self.error = message
            AppLifecycleLog.error("project", "createProject failed repo=\(trimmedRepo): \(message)")
            return nil
        }
    }

    private static func isAlreadyExistsError(_ message: String) -> Bool {
        let m = message.lowercased()
        return m.contains("already exists") || m.contains("duplicate")
    }

    private func recoverExistingProject(repo: String) async -> Project? {
        do {
            let all = try await client.listProjects()
            let normalizedRepo = normalizeRepo(repo)
            let targetName = projectNameFromRepo(repo)
            if let exact = all.first(where: { normalizeRepo($0.primaryRepo) == normalizedRepo }) {
                return exact
            }
            if let byName = all.first(where: { $0.name.caseInsensitiveCompare(targetName) == .orderedSame }) {
                return byName
            }
            return nil
        } catch {
            AppLifecycleLog.error("project", "recoverExistingProject failed: \(error.localizedDescription)")
            return nil
        }
    }

    private func normalizeRepo(_ raw: String) -> String {
        raw.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            .lowercased()
    }

    private func projectNameFromRepo(_ repo: String) -> String {
        var name = repo.trimmingCharacters(in: .whitespacesAndNewlines)
        if let slash = name.lastIndex(of: "/") {
            name = String(name[name.index(after: slash)...])
        }
        if let colon = name.lastIndex(of: ":") {
            name = String(name[name.index(after: colon)...])
        }
        if name.hasSuffix(".git") {
            name = String(name.dropLast(4))
        }
        return name.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    public func start(_ workspace: Workspace) async {
        await perform(workspaceID: workspace.id, opState: .starting(detail: nil)) {
            try await self.client.startWorkspace(id: workspace.id)
            // Daemon returns immediately with state=starting; poll until running or rolled-back.
            let deadline = Date().addingTimeInterval(15 * 60) // 15 min ceiling
            while Date() < deadline {
                try await Task.sleep(nanoseconds: 2_000_000_000) // 2 s
                let all = try await self.client.listWorkspaces()
                guard let ws = all.first(where: { $0.id == workspace.id }) else { return }
                if ws.state != .starting { return }
            }
        }
    }

    public func stop(_ workspace: Workspace) async {
        await perform(workspaceID: workspace.id, opState: .stopping) {
            try await self.client.stopWorkspace(id: workspace.id)
        }
    }

    public func remove(_ workspace: Workspace) async {
        if selectedWorkspaceID == workspace.id { selectedWorkspaceID = nil }
        await perform(workspaceID: workspace.id, opState: .removing) {
            // Daemon refuses to remove a running workspace; stop it first.
            if workspace.status.isActive {
                try await self.client.stopWorkspace(id: workspace.id)
            }
            try await self.client.removeWorkspace(id: workspace.id)
        }
    }

    // MARK: - Sync actions

    public func startSync(workspaceID: String, localPath: String, direction: String) async {
        await performSync(workspaceID: workspaceID, opState: .starting) {
            let session = try await self.client.startSync(workspaceID: workspaceID, localPath: localPath, direction: direction)
            await self.refreshSyncs(workspaceID: workspaceID)
            return session
        }
    }

    public func stopSync(sessionID: String, workspaceID: String) async {
        await performSync(workspaceID: workspaceID, opState: .stopping) {
            try await self.client.stopSync(sessionID: sessionID, workspaceID: workspaceID)
            await self.refreshSyncs(workspaceID: workspaceID)
        }
    }

    public func pauseSync(sessionID: String, workspaceID: String) async {
        await performSync(workspaceID: workspaceID, opState: .pausing) {
            try await self.client.pauseSync(sessionID: sessionID)
            await self.refreshSyncs(workspaceID: workspaceID)
        }
    }

    public func resumeSync(sessionID: String, workspaceID: String) async {
        await performSync(workspaceID: workspaceID, opState: .resuming) {
            try await self.client.resumeSync(sessionID: sessionID)
            await self.refreshSyncs(workspaceID: workspaceID)
        }
    }

    public func refreshSyncs(workspaceID: String) async {
        do {
            let sessions = try await client.listSyncs(workspaceID: workspaceID)
            syncSessions[workspaceID] = sessions
        } catch {
            Self.logger.error("refreshSyncs failed: \(error.localizedDescription, privacy: .public)")
        }
    }

    // MARK: - Sync helper

    @discardableResult
    private func performSync<T>(workspaceID: String, opState: SyncOpState, _ op: @escaping () async throws -> T) async -> T? {
        syncOps[workspaceID] = opState
        defer { syncOps.removeValue(forKey: workspaceID) }
        do {
            let result = try await op()
            return result
        } catch {
            self.error = error.localizedDescription
            return nil
        }
    }

    /// Removes a daemon project record and refreshes lists.
    /// The daemon cascades: stops and removes all associated workspaces (and their VMs)
    /// before deleting the project record.
    public func removeProject(id: String) async {
        let wsIds = Set(repos.first(where: { $0.id == id })?.workspaces.map(\.id) ?? [])
        if let sel = selectedWorkspaceID, wsIds.contains(sel) {
            selectedWorkspaceID = nil
        }
        await perform { try await self.client.removeProject(id: id) }
    }

    /// Removes multiple daemon project records by id.
    /// The daemon cascades workspace cleanup for each project.
    public func removeProjects(ids: [String]) async {
        let targetIDs = Set(ids.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty })
        guard !targetIDs.isEmpty else {
            self.error = "No project IDs to delete."
            return
        }

        let wsIds = Set(
            repos
                .filter { targetIDs.contains($0.id) }
                .flatMap { $0.workspaces.map(\.id) }
        )
        if let sel = selectedWorkspaceID, wsIds.contains(sel) {
            selectedWorkspaceID = nil
        }

        await perform {
            for id in targetIDs {
                try await self.client.removeProject(id: id)
            }
        }
    }

    /// Removes daemon project(s) by display name, useful when sidebar repo groups are
    /// not directly mapped to a local `projectID` (e.g. stale/unlinked project rows).
    public func removeProjects(named name: String, allMatches: Bool = true) async {
        let target = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !target.isEmpty else {
            self.error = "Project name is empty."
            return
        }

        let matchingRepoWorkspaceIDs: Set<String> = Set(
            repos
                .filter { $0.name.localizedCaseInsensitiveCompare(target) == .orderedSame }
                .flatMap { $0.workspaces.map(\.id) }
        )
        if let sel = selectedWorkspaceID, matchingRepoWorkspaceIDs.contains(sel) {
            selectedWorkspaceID = nil
        }

        await perform {
            let projects = try await self.client.listProjects()
            let matches = projects.filter {
                $0.name.trimmingCharacters(in: .whitespacesAndNewlines)
                    .localizedCaseInsensitiveCompare(target) == .orderedSame
            }
            guard !matches.isEmpty else {
                throw RPCError(message: "No daemon project named '\(target)'")
            }
            let targets = allMatches ? matches : [matches[0]]
            for project in targets {
                try await self.client.removeProject(id: project.id)
            }
        }
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
            // Kill any existing spotlight run process for this workspace first.
            self.killSpotlightCLI(workspaceID: workspace.id)
            // Launch long-lived `nexus spotlight run <workspaceId>`.
            // The process owns the SSH tunnel and calls spotlight.stop on exit.
            // We scan stdout for the "forwarded N/N ports" line, then return.
            let output = try await self.runSpotlightRun(workspaceID: workspace.id)
            Self.logger.info("spotlight run output: \(output, privacy: .public)")
        }
    }

    public func stopTunnels(_ workspace: Workspace) async {
        await perform {
            self.killSpotlightCLI(workspaceID: workspace.id)
            Self.logger.info("spotlight run process stopped for \(workspace.id, privacy: .public)")
        }
    }

    /// Run a nexus CLI subcommand with the current daemon connection injected via env vars.
    /// Returns combined stdout+stderr. Throws if the process exits non-zero.
    @discardableResult
    private func runSpotlightCLI(args: [String]) async throws -> String {
        // This was used for SSH connectivity check and other short CLI operations.
        // Since client is already connected to daemon, connectivity is verified.
        // For SSH-specific checks, we can test SSH connectivity directly.
        if args.contains("ssh") && args.contains("check") {
            let sshHost = cachedProfile?.sshTarget ?? ""
            let sshPort = cachedProfile?.sshPort ?? 22
            let identity = cachedProfile?.sshIdentity ?? ""
            return try await withCheckedThrowingContinuation { cont in
                DispatchQueue.global(qos: .userInitiated).async {
                    let proc = Process()
                    proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
                    proc.arguments = [
                        "-o", "StrictHostKeyChecking=accept-new",
                        "-o", "ConnectTimeout=5",
                        "-p", String(sshPort),
                        "-i", identity,
                        sshHost, "echo ok"
                    ]
                    let outPipe = Pipe()
                    let errPipe = Pipe()
                    proc.standardOutput = outPipe
                    proc.standardError = errPipe
                    do {
                        try proc.run()
                        proc.waitUntilExit()
                        let out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                        let err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                        if proc.terminationStatus == 0 {
                            cont.resume(returning: "SSH connection successful: \(out.trimmingCharacters(in: .whitespacesAndNewlines))")
                        } else {
                            cont.resume(throwing: RPCError(message: "SSH check failed (exit \(proc.terminationStatus)): \(err.trimmingCharacters(in: .whitespacesAndNewlines))"))
                        }
                    } catch {
                        cont.resume(throwing: error)
                    }
                }
            }
        }
        // For any other args, return success (daemon is connected)
        return "connected"
    }

    @discardableResult
    private func runSpotlightRun(workspaceID: String) async throws -> String {
        let sshHost = cachedProfile?.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let sshPort = cachedProfile?.sshPort ?? 22
        let sshIdentity = cachedProfile?.sshIdentity?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

        // 1. Discover ports
        let ports = try await spotlightManager.discoverPorts(workspaceID: workspaceID)
        guard !ports.isEmpty else {
            throw RPCError(message: "no discoverable ports for workspace \(workspaceID)")
        }

        // 2. Start spotlight on daemon
        try await spotlightManager.startSpotlight(workspaceID: workspaceID, ports: ports)

        // 3. Start SSH tunnels
        try await spotlightManager.startDaemonTunnels(
            workspaceID: workspaceID,
            host: sshHost,
            sshPort: sshPort,
            identityFile: sshIdentity
        )

        return "forwarded \(ports.count)/\(ports.count) ports"
    }

    private func killSpotlightCLI(workspaceID: String) {
        // Kill SSH tunnels
        spotlightManager.stopSSHTunnels(workspaceID: workspaceID)
        // Stop spotlight on daemon
        Task {
            try? await spotlightManager.stopSpotlight(workspaceID: workspaceID)
        }
        // Clean up legacy PID tracking
        if let pid = spotlightCLIProcesses.removeValue(forKey: workspaceID) {
            kill(pid, SIGTERM)
        }
    }

    private func perform(workspaceID: String? = nil, opState: WorkspaceOpState? = nil, _ op: @escaping () async throws -> Void) async {
        if let id = workspaceID, let state = opState {
            workspaceOps[id] = state
        }
        defer {
            if let id = workspaceID {
                workspaceOps.removeValue(forKey: id)
            }
        }
        do {
            try await op()
            await load()
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Poll `listWorkspaces` until the workspace leaves the `starting` state (running,
    /// stopped, created, or gone), then clear `workspaceOps` and `workspaceCreateProgress`.
    ///
    /// This replaces the old log-scraping monitor.  The daemon documents that callers
    /// should poll workspace state rather than rely on log content, so we do exactly that.
    /// The 15-minute ceiling matches the existing `start()` polling ceiling.
    private func pollUntilStarted(workspaceID: String, workspaceName: String) {
        workspaceCreateMonitorTasks[workspaceID]?.cancel()
        let daemonClient = client
        let startedAt = Date()

        workspaceCreateMonitorTasks[workspaceID] = Task.detached(priority: .utility) { [weak self] in
            guard let self else { return }
            defer {
                Task { @MainActor [weak self] in
                    guard let self else { return }
                    self.workspaceCreateMonitorTasks.removeValue(forKey: workspaceID)
                    // Always clear the op and progress so the sidebar never stays stuck.
                    self.workspaceOps.removeValue(forKey: workspaceID)
                    self.workspaceCreateProgress = nil
                }
            }

            let deadline = Date().addingTimeInterval(15 * 60)
            while Date() < deadline {
                if Task.isCancelled { return }

                do {
                    let workspaces = try await daemonClient.listWorkspaces()
                    let ws = workspaces.first(where: { $0.id == workspaceID })
                    let wsState = ws?.state
                    let elapsed = Date().timeIntervalSince(startedAt)

                    let phaseLabel: String
                    let done: Bool
                    switch wsState {
                    case .running, .restored:
                        phaseLabel = "Ready"
                        done = true
                    case .starting:
                        phaseLabel = "Starting"
                        done = false
                    case .stopped, .created, .none:
                        // Workspace rolled back or was deleted — treat as done so UI clears.
                        phaseLabel = "Stopped"
                        done = true
                    default:
                        phaseLabel = wsState?.rawValue.capitalized ?? "Starting"
                        done = false
                    }

                    let progress = WorkspaceCreateProgress(
                        workspaceID: workspaceID,
                        workspaceName: workspaceName,
                        elapsedSeconds: max(0, elapsed),
                        currentPhaseLabel: phaseLabel,
                        phaseTimings: [],
                        notes: [],
                        isComplete: done
                    )

                    await MainActor.run { [weak self] in
                        guard let self else { return }
                        self.workspaceCreateProgress = progress
                        if done {
                            self.workspaceOps.removeValue(forKey: workspaceID)
                        } else {
                            self.workspaceOps[workspaceID] = .starting(detail: "\(phaseLabel) (\(String(format: "%.0f", elapsed))s)")
                        }
                    }

                    if done { return }
                } catch {
                    // Transient tunnel/daemon errors are expected during startup — keep polling.
                }

                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
            // Ceiling reached — defer will clear workspaceOps and progress.
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

    private nonisolated static func provisioningStatusMessage(phase: String, message: String) -> String {
        let normalizedMessage = message.trimmingCharacters(in: .whitespacesAndNewlines)
        let normalizedPhase = phase.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()

        if normalizedPhase == "rootfs-bake" {
            if normalizedMessage.contains("pre-baking rootfs toolchain") {
                return "Baking base image (installing toolchain)…"
            }
            if normalizedMessage.contains("checking cached rootfs bake") {
                return "Checking cached base image…"
            }
            if normalizedMessage.contains("base rootfs bake ready") {
                return "Base image ready"
            }
            if normalizedMessage.contains("bake failed") {
                return "Base image bake failed (fallback to first-boot install)"
            }
            if normalizedMessage.contains("skipped") {
                return "Base image bake skipped"
            }
        }

        if normalizedMessage.isEmpty {
            return "Bootstrapping…"
        }
        return normalizedMessage
    }
}

public enum SyncOpState: Equatable {
    case starting
    case stopping
    case pausing
    case resuming
}

public enum ConnectionState: Equatable {
    case starting, disconnected, connecting, connected
    /// Auto-provisioning a fresh remote host (uploading binary, starting daemon).
    case provisioning(step: String)
}

/// The compatibility status of the running daemon.
public enum DaemonStatus: Equatable {
    case unknown
    case running(info: DaemonInfo)
    case outdated(running: DaemonInfo)  // protocolVersion < requiredProtocol
    case offline
}
