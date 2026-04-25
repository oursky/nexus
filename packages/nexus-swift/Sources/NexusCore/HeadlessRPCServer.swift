import Foundation
import Network
import OSLog

/// A minimal local HTTP/1.1 server that exposes headless control over terminal tabs,
/// workspace lifecycle, and remote provisioning for automated end-to-end testing.
/// Only active when NEXUS_HEADLESS_RPC=1 or ~/.nexus-headless-rpc sentinel file exists.
///
/// Terminal endpoints:
///   GET  /status
///   GET  /terminal/tabs
///   POST /terminal/open         { workspaceID, name? }
///   POST /terminal/write        { tabID, text }
///   GET  /terminal/read?tabID=...
///   POST /terminal/clear        { tabID }
///
/// Workspace lifecycle (maps to daemon client):
///   GET  /workspace/list
///   POST /workspace/create      { name, repo, ref?, backend? }
///   POST /workspace/start       { workspaceID }
///   POST /workspace/stop        { workspaceID }
///   POST /workspace/delete      { workspaceID }
///   GET  /workspace/info?workspaceID=...
///
/// Editor / SSH:
///   POST /workspace/ssh-check   { workspaceID }  → { ok, detail }
///   POST /workspace/open-editor { workspaceID, app? }  → { ok, detail }
///
/// Daemon provisioning (simulates full fresh-host onboarding):
///   GET  /daemon/status
///   POST /daemon/provision      { sshTarget, port?, sshIdentity? }
///   POST /daemon/connect        { sshTarget, port?, sshIdentity? }
///
/// Linuxbox clean-room (remote state removal for regression testing):
///   POST /linuxbox/clean-room   { sshTarget, sshPort? }  — remove ~/.local/{share,state,run}/nexus + binaries
@MainActor
public final class HeadlessRPCServer {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "HeadlessRPCServer")
    public static let defaultPort: UInt16 = 7778

    private var listener: NWListener?
    private let port: UInt16
    private let clientProvider: () -> WebSocketDaemonClient?

    /// Called by /workspace/ssh-check and /workspace/open-editor.
    /// (workspaceID, app, checkOnly) → (ok, detail)
    public var openEditorAction: ((String, String, Bool) async -> (Bool, String))?

    /// Called by /daemon/check. (driver?) → (allPassed, output)
    public var daemonCheckAction: ((String?) async -> (Bool, String))?

    /// Returns the active daemon client (any protocol) for workspace lifecycle calls.
    public var daemonClientProvider: (() -> (any DaemonClient)?)?

    /// Returns the active daemon profile for provisioning calls.
    public var daemonProfileProvider: (() -> DaemonProfile?)?

    public init(
        port: UInt16 = HeadlessRPCServer.defaultPort,
        clientProvider: @escaping () -> WebSocketDaemonClient? = { nil }
    ) {
        self.port = port
        self.clientProvider = clientProvider
    }

    public func start() {
        let env = ProcessInfo.processInfo.environment
        let envEnabled = env["NEXUS_HEADLESS_RPC"] == "1"
        // Also allow activation via a sentinel file (for GUI apps where env var may not propagate
        // due to macOS app sandbox container isolation).
        let sentinelPath = (NSHomeDirectory() as NSString).appendingPathComponent(".nexus-headless-rpc")
        let fileEnabled = FileManager.default.fileExists(atPath: sentinelPath)
        guard envEnabled || fileEnabled else {
            Self.logger.debug("rpc.server disabled (set NEXUS_HEADLESS_RPC=1 or touch \(sentinelPath, privacy: .public))")
            return
        }
        Self.logger.notice("rpc.server starting on port \(self.port, privacy: .public)")
        do {
            let params = NWParameters.tcp
            params.allowLocalEndpointReuse = true
            let nwPort = NWEndpoint.Port(rawValue: port)!
            let listener = try NWListener(using: params, on: nwPort)
            self.listener = listener

            listener.newConnectionHandler = { [weak self] conn in
                Task { @MainActor [weak self] in
                    self?.handleConnection(conn)
                }
            }
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    Self.logger.notice("rpc.server listening on 127.0.0.1:\(HeadlessRPCServer.defaultPort, privacy: .public)")
                case .failed(let err):
                    Self.logger.error("rpc.server failed: \(err.localizedDescription, privacy: .public)")
                default:
                    break
                }
            }
            listener.start(queue: .main)
        } catch {
            Self.logger.error("rpc.server start error: \(error.localizedDescription, privacy: .public)")
        }
    }

    public func stop() {
        listener?.cancel()
        listener = nil
    }

    // MARK: - Connection handling

    private func handleConnection(_ conn: NWConnection) {
        // Reject connections from non-loopback addresses for security.
        let remote = conn.endpoint
        if !isLoopback(remote) {
            Self.logger.warning("rpc.server rejected non-loopback connection from \(String(describing: remote), privacy: .public)")
            conn.cancel()
            return
        }
        conn.start(queue: .main)
        receiveRequest(conn)
    }

    private func isLoopback(_ endpoint: NWEndpoint) -> Bool {
        switch endpoint {
        case .hostPort(let host, _):
            switch host {
            case .ipv4(let addr):
                // 127.0.0.0/8
                return addr == IPv4Address.loopback || String(describing: addr).hasPrefix("127.")
            case .ipv6(let addr):
                return addr == IPv6Address.loopback
            case .name(let name, _):
                return name == "localhost" || name == "127.0.0.1" || name == "::1"
            @unknown default:
                return false
            }
        default:
            return false
        }
    }

    private func receiveRequest(_ conn: NWConnection) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 65_536) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            if let data, !data.isEmpty, let raw = String(data: data, encoding: .utf8) {
                Task { @MainActor in
                    let (status, body) = await self.routeRequest(raw)
                    self.sendResponse(conn, status: status, body: body)
                }
            } else {
                conn.cancel()
            }
        }
    }

    // MARK: - Routing

    private func routeRequest(_ raw: String) async -> (Int, String) {
        // Parse first line: METHOD path HTTP/1.1
        let lines = raw.components(separatedBy: "\r\n")
        guard let requestLine = lines.first else {
            return (400, jsonError("bad request"))
        }
        let parts = requestLine.components(separatedBy: " ")
        guard parts.count >= 2 else {
            return (400, jsonError("bad request"))
        }
        let method = parts[0].uppercased()
        let rawPath = parts[1]  // may include query string

        // Split path and query
        let pathParts = rawPath.components(separatedBy: "?")
        let path = pathParts[0]
        let query = pathParts.count > 1 ? pathParts[1] : ""

        // Extract body (after blank line)
        var bodyString = ""
        if let blankIdx = lines.firstIndex(of: "") {
            bodyString = lines[(blankIdx + 1)...].joined(separator: "\r\n")
        }

        switch (method, path) {
        case ("GET", "/status"):
            return handleStatus()

        // ── Terminal ──────────────────────────────────────────────────────────
        case ("GET", "/terminal/tabs"):
            return await handleListTabs()
        case ("POST", "/terminal/open"):
            return await handleOpen(body: bodyString)
        case ("POST", "/terminal/write"):
            return await handleWrite(body: bodyString)
        case ("GET", "/terminal/read"):
            return await handleRead(query: query)
        case ("POST", "/terminal/clear"):
            return await handleClear(body: bodyString)

        // ── Workspace lifecycle ───────────────────────────────────────────────
        case ("GET", "/workspace/list"):
            return await handleWorkspaceList()
        case ("POST", "/workspace/create"):
            return await handleWorkspaceCreate(body: bodyString)
        case ("POST", "/workspace/start"):
            return await handleWorkspaceStart(body: bodyString)
        case ("POST", "/workspace/stop"):
            return await handleWorkspaceStop(body: bodyString)
        case ("POST", "/workspace/delete"):
            return await handleWorkspaceDelete(body: bodyString)
        case ("GET", "/workspace/info"):
            return await handleWorkspaceInfo(query: query)

        // ── Editor / SSH ──────────────────────────────────────────────────────
        case ("POST", "/workspace/ssh-check"):
            return await handleSSHCheck(body: bodyString)
        case ("POST", "/workspace/open-editor"):
            return await handleOpenEditor(body: bodyString)

        // ── Daemon provisioning ───────────────────────────────────────────────
        case ("GET", "/daemon/status"):
            return await handleDaemonStatus()
        case ("POST", "/daemon/provision"):
            return await handleDaemonProvision(body: bodyString)
        case ("POST", "/daemon/connect"):
            return await handleDaemonConnect(body: bodyString)
        case ("POST", "/daemon/check"):
            return await handleDaemonCheck(body: bodyString)

        // ── Linuxbox clean-room ───────────────────────────────────────────────
        case ("POST", "/linuxbox/clean-room"):
            return await handleLinuxboxCleanRoom(body: bodyString)

        default:
            return (404, jsonError("not found"))
        }
    }

    // MARK: - Handlers

    private func handleStatus() -> (Int, String) {
        let body = #"{"ok":true,"version":"1"}"#
        return (200, body)
    }

    private func handleListTabs() async -> (Int, String) {
        let managers = TerminalRegistry.shared.allManagers
        var tabList: [[String: Any]] = []
        for mgr in managers {
            for tab in mgr.tabs {
                tabList.append([
                    "id": tab.id,
                    "name": tab.name,
                    "workspaceID": mgr.workspaceId,
                    "isActive": tab.isActive,
                    "isLoading": tab.isLoading,
                    "error": tab.error as Any
                ])
            }
        }
        guard let data = try? JSONSerialization.data(withJSONObject: ["tabs": tabList]),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (200, str)
    }

    private func handleOpen(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let workspaceID = dict["workspaceID"] as? String else {
            return (400, jsonError("missing workspaceID"))
        }
        let name = dict["name"] as? String

        let mgr: PTYSessionManager
        if let existing = TerminalRegistry.shared.manager(for: workspaceID) {
            mgr = existing
        } else if let client = clientProvider() {
            // Headless and UI should share the same viewmodel path. If UI has not opened
            // this workspace yet, create the same PTYSessionManager here.
            mgr = TerminalRegistry.shared.ensureManager(workspaceId: workspaceID, client: client)
        } else {
            return (503, jsonError("daemon client unavailable"))
        }

        let before = Set(mgr.tabs.map { $0.id })
        await mgr.createTab(name: name)
        // If tab creation failed, surface the same error the UI would show.
        if let errTab = mgr.tabs.last, errTab.error != nil {
            return (500, jsonError(errTab.error ?? "unknown error"))
        }

        // Pick the newly created tab (or active tab as fallback).
        if let created = mgr.tabs.first(where: { !before.contains($0.id) }) ??
            mgr.tabs.first(where: { $0.id == mgr.activeTabId })
        {
            return (200, #"{"tabID":"\#(created.id)"}"#)
        }

        return (500, jsonError("tab creation failed"))
    }

    private func handleWrite(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let tabID = dict["tabID"] as? String,
              let text = dict["text"] as? String else {
            return (400, jsonError("missing tabID or text"))
        }

        // Find the client from any registered manager that has this tab
        guard let (mgr, _) = findTab(tabID) else {
            return (404, jsonError("tab not found"))
        }

        do {
            try await mgr.writePTY(sessionId: tabID, text: text)
            return (200, #"{"ok":true}"#)
        } catch {
            return (500, jsonError(error.localizedDescription))
        }
    }

    private func handleRead(query: String) async -> (Int, String) {
        let params = parseQuery(query)
        guard let tabID = params["tabID"] else {
            return (400, jsonError("missing tabID query param"))
        }
        let output = TerminalRegistry.shared.drainOutput(for: tabID) ?? ""
        guard let data = try? JSONSerialization.data(withJSONObject: ["tabID": tabID, "output": output]),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (200, str)
    }

    private func handleClear(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let tabID = dict["tabID"] as? String else {
            return (400, jsonError("missing tabID"))
        }
        TerminalRegistry.shared.clearOutput(for: tabID)
        return (200, #"{"ok":true}"#)
    }

    private func handleSSHCheck(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let workspaceID = dict["workspaceID"] as? String else {
            return (400, jsonError("missing workspaceID"))
        }
        guard let action = openEditorAction else {
            return (503, jsonError("open-editor action not registered"))
        }
        Self.logger.info("rpc /workspace/ssh-check workspaceID=\(workspaceID, privacy: .public)")
        let (ok, detail) = await action(workspaceID, "cursor", true)
        let payload: [String: Any] = ["ok": ok, "detail": detail]
        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (ok ? 200 : 500, str)
    }

    private func handleOpenEditor(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let workspaceID = dict["workspaceID"] as? String else {
            return (400, jsonError("missing workspaceID"))
        }
        let app = dict["app"] as? String ?? "cursor"
        guard let action = openEditorAction else {
            return (503, jsonError("open-editor action not registered"))
        }
        Self.logger.info("rpc /workspace/open-editor workspaceID=\(workspaceID, privacy: .public) app=\(app, privacy: .public)")
        let (ok, detail) = await action(workspaceID, app, false)
        let payload: [String: Any] = ["ok": ok, "detail": detail]
        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (ok ? 200 : 500, str)
    }

    // MARK: - Workspace lifecycle handlers

    private func handleWorkspaceList() async -> (Int, String) {
        guard let client = daemonClientProvider?() else {
            return (503, jsonError("daemon client unavailable"))
        }
        do {
            let workspaces = try await client.listWorkspaces()
            let list: [[String: Any]] = workspaces.map { ws in
                var d: [String: Any] = [
                    "id": ws.id,
                    "name": ws.workspaceName,
                    "repo": ws.repo,
                    "ref": ws.ref,
                    "state": ws.state.rawValue,
                    "rootPath": ws.rootPath,
                ]
                if let backend = ws.backend { d["backend"] = backend }
                if let guestIp = ws.guestIp { d["guestIp"] = guestIp }
                return d
            }
            guard let data = try? JSONSerialization.data(withJSONObject: ["workspaces": list]),
                  let str = String(data: data, encoding: .utf8) else {
                return (500, jsonError("serialization failed"))
            }
            return (200, str)
        } catch {
            return (500, jsonError(error.localizedDescription))
        }
    }

    private func handleWorkspaceCreate(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let name = dict["name"] as? String,
              let repo = dict["repo"] as? String else {
            return (400, jsonError("missing required fields: name, repo"))
        }
        guard let client = daemonClientProvider?() else {
            return (503, jsonError("daemon client unavailable"))
        }
        let ref = dict["ref"] as? String ?? "main"
        let backend = dict["backend"] as? String ?? ""
        let spec = WorkspaceCreateSpec(
            repo: repo,
            ref: ref,
            workspaceName: name,
            agentProfile: "default",
            backend: backend
        )
        do {
            let ws = try await client.createWorkspace(spec: spec)
            let payload: [String: Any] = [
                "workspaceID": ws.id,
                "name": ws.workspaceName,
                "state": ws.state.rawValue,
            ]
            guard let data = try? JSONSerialization.data(withJSONObject: payload),
                  let str = String(data: data, encoding: .utf8) else {
                return (500, jsonError("serialization failed"))
            }
            return (200, str)
        } catch {
            return (500, jsonError(error.localizedDescription))
        }
    }

    private func handleWorkspaceStart(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let workspaceID = dict["workspaceID"] as? String else {
            return (400, jsonError("missing workspaceID"))
        }
        guard let client = daemonClientProvider?() else {
            return (503, jsonError("daemon client unavailable"))
        }
        do {
            try await client.startWorkspace(id: workspaceID)
            return (200, #"{"ok":true}"#)
        } catch {
            return (500, jsonError(error.localizedDescription))
        }
    }

    private func handleWorkspaceStop(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let workspaceID = dict["workspaceID"] as? String else {
            return (400, jsonError("missing workspaceID"))
        }
        guard let client = daemonClientProvider?() else {
            return (503, jsonError("daemon client unavailable"))
        }
        do {
            try await client.stopWorkspace(id: workspaceID)
            return (200, #"{"ok":true}"#)
        } catch {
            return (500, jsonError(error.localizedDescription))
        }
    }

    private func handleWorkspaceDelete(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let workspaceID = dict["workspaceID"] as? String else {
            return (400, jsonError("missing workspaceID"))
        }
        guard let client = daemonClientProvider?() else {
            return (503, jsonError("daemon client unavailable"))
        }
        do {
            try await client.removeWorkspace(id: workspaceID)
            return (200, #"{"ok":true}"#)
        } catch {
            return (500, jsonError(error.localizedDescription))
        }
    }

    private func handleWorkspaceInfo(query: String) async -> (Int, String) {
        let params = parseQuery(query)
        guard let workspaceID = params["workspaceID"] else {
            return (400, jsonError("missing workspaceID query param"))
        }
        guard let client = daemonClientProvider?() else {
            return (503, jsonError("daemon client unavailable"))
        }
        do {
            let info = try await client.workspaceInfo(id: workspaceID)
            let payload: [String: Any] = [
                "workspaceID": workspaceID,
                "workspacePath": info.workspacePath,
                "ports": info.ports.map { ["local": $0.id, "remote": $0.remotePort] },
            ]
            guard let data = try? JSONSerialization.data(withJSONObject: payload),
                  let str = String(data: data, encoding: .utf8) else {
                return (500, jsonError("serialization failed"))
            }
            return (200, str)
        } catch {
            return (500, jsonError(error.localizedDescription))
        }
    }

    // MARK: - Daemon provisioning handlers

    private func handleDaemonStatus() async -> (Int, String) {
        let profile = daemonProfileProvider?()
        let hasProfile = profile != nil
        let sshTarget = profile?.sshTarget ?? ""
        let payload: [String: Any] = [
            "hasProfile": hasProfile,
            "sshTarget": sshTarget,
            "port": profile?.port ?? 7777,
            "clientConnected": daemonClientProvider?() != nil,
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (200, str)
    }

    private func handleDaemonProvision(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let sshTarget = dict["sshTarget"] as? String else {
            return (400, jsonError("missing sshTarget"))
        }
        let port = dict["port"] as? Int ?? 7777
        let sshIdentity = dict["sshIdentity"] as? String
        let sshPort = dict["sshPort"] as? Int

        let profile = DaemonProfile(
            name: "headless-rpc",
            port: port,
            sshTarget: sshTarget,
            sshPort: sshPort,
            sshIdentity: sshIdentity
        )
        let provisioner = RemoteProvisioner(profile: profile)
        var phases: [[String: String]] = []
        do {
            try await provisioner.provision { step in
                switch step {
                case .checkingHost:
                    phases.append(["step": "checking-host"])
                case .uploadingBinary(let pct):
                    phases.append(["step": "uploading-binary", "progress": String(format: "%.0f%%", pct * 100)])
                case .startingDaemon:
                    phases.append(["step": "starting-daemon"])
                case .bootstrapPhase(let phase, let msg):
                    phases.append(["step": "bootstrap", "phase": phase, "message": msg])
                case .waitingForDaemon(let attempt):
                    phases.append(["step": "waiting", "attempt": "\(attempt)"])
                case .ready:
                    phases.append(["step": "ready"])
                }
            }
            let payload: [String: Any] = ["ok": true, "phases": phases]
            guard let data = try? JSONSerialization.data(withJSONObject: payload),
                  let str = String(data: data, encoding: .utf8) else {
                return (500, jsonError("serialization failed"))
            }
            return (200, str)
        } catch {
            let payload: [String: Any] = ["ok": false, "error": error.localizedDescription, "phases": phases]
            guard let data = try? JSONSerialization.data(withJSONObject: payload),
                  let str = String(data: data, encoding: .utf8) else {
                return (500, jsonError(error.localizedDescription))
            }
            return (500, str)
        }
    }

    private func handleDaemonConnect(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let sshTarget = dict["sshTarget"] as? String else {
            return (400, jsonError("missing sshTarget"))
        }
        let port = dict["port"] as? Int ?? 7777
        let sshIdentity = dict["sshIdentity"] as? String
        let sshPort = dict["sshPort"] as? Int

        let profile = DaemonProfile(
            name: "headless-rpc",
            port: port,
            sshTarget: sshTarget,
            sshPort: sshPort,
            sshIdentity: sshIdentity
        )
        let provisioner = RemoteProvisioner(profile: profile)
        do {
            try await provisioner.provision()
        } catch {
            return (500, jsonError("provision failed: \(error.localizedDescription)"))
        }

        let payload: [String: Any] = [
            "ok": true,
            "sshTarget": sshTarget,
            "port": port,
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (200, str)
    }

    // MARK: - Daemon health-check handler

    /// Runs `nexus daemon check [--driver <driver>]` and returns all check results.
    ///   POST /daemon/check  { driver? }  → { ok, output }
    private func handleDaemonCheck(body: String) async -> (Int, String) {
        let dict = parseJSON(body)
        let driver = dict?["driver"] as? String
        guard let action = daemonCheckAction else {
            return (503, jsonError("daemon check action not registered"))
        }
        Self.logger.info("rpc /daemon/check driver=\(driver ?? "(auto)", privacy: .public)")
        let (ok, output) = await action(driver)
        let payload: [String: Any] = ["ok": ok, "output": output]
        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (ok ? 200 : 500, str)
    }

    // MARK: - Linuxbox clean-room handler

    /// Runs the remote clean-room fixture: removes nexus state and binaries.
    /// This enables regression testing from absolute clean state without SSH manually.
    private func handleLinuxboxCleanRoom(body: String) async -> (Int, String) {
        guard let dict = parseJSON(body),
              let sshTarget = dict["sshTarget"] as? String else {
            return (400, jsonError("missing sshTarget"))
        }
        let sshPort = dict["sshPort"] as? Int ?? 22
        let sshIdentity = dict["sshIdentity"] as? String

        var args = [
            "-p", "\(sshPort)",
            "-F", "/dev/null",
            "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null",
            "-o", "GlobalKnownHostsFile=/dev/null",
            "-o", "ConnectTimeout=15",
        ]
        if let identity = sshIdentity, !identity.isEmpty {
            args += ["-i", identity]
        }

        // Pipe the clean-room script via stdin to `bash -s` to avoid:
        // - SIGPIPE from broken stdout pipe (trap '' PIPE + exec to /dev/null)
        // - quoting complexity of inline command strings
        // - `set -e` interaction with `test && echo` patterns
        let cleanRoomScript = """
            trap '' PIPE
            set -uo pipefail
            echo "==> Stopping all nexus daemon processes..."
            pkill -f "nexus daemon" 2>/dev/null || true
            pkill -f "nexus-libkrun" 2>/dev/null || true
            sleep 2
            echo "==> Removing nexus state..."
            rm -rf "$HOME/.local/share/nexus" || true
            rm -rf "$HOME/.local/state/nexus" || true
            rm -rf "$HOME/.local/run/nexus" || true
            rm -f "$HOME/.local/bin/nexus" || true
            rm -f "$HOME/.local/bin/nexus-libkrun" || true
            rm -f "$HOME/.local/bin/nexus-guest-agent" || true
            rm -f "$HOME/.local/bin/passt" || true
            rm -f "$HOME/.local/bin/pasta" || true
            echo "==> Verifying removal..."
            if [ ! -d "$HOME/.local/share/nexus" ]; then echo "share/nexus: removed"; else echo "share/nexus: still present"; fi
            if [ ! -d "$HOME/.local/state/nexus" ]; then echo "state/nexus: removed"; else echo "state/nexus: still present"; fi
            if [ ! -f "$HOME/.local/bin/nexus" ]; then echo "nexus binary: removed"; else echo "nexus binary: still present"; fi
            echo "==> Clean-room complete"
            """

        // Pass script via stdin to `bash -s` to avoid quoting/SIGPIPE issues.
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
        proc.arguments = args + [sshTarget, "bash -s"]

        let stdinPipe = Pipe()
        proc.standardInput = stdinPipe
        if let scriptData = cleanRoomScript.data(using: .utf8) {
            stdinPipe.fileHandleForWriting.write(scriptData)
            stdinPipe.fileHandleForWriting.closeFile()
        }

        // Forward SSH_AUTH_SOCK so SSH key agent works from sandboxed context.
        var env = ProcessInfo.processInfo.environment
        if let authSock = env["SSH_AUTH_SOCK"], !authSock.isEmpty {
            env["SSH_AUTH_SOCK"] = authSock
        }
        proc.environment = env

        let outPipe = Pipe()
        let errPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = errPipe

        // Drain both pipes concurrently to prevent buffer deadlock when the process
        // produces output faster than the caller reads (pipe buffer ~64 KB on macOS).
        nonisolated(unsafe) var out = ""
        nonisolated(unsafe) var err = ""
        let group = DispatchGroup()
        group.enter()
        DispatchQueue.global().async {
            out = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            group.leave()
        }
        group.enter()
        DispatchQueue.global().async {
            err = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            group.leave()
        }

        do {
            try proc.run()
        } catch {
            return (500, jsonError("ssh failed: \(error.localizedDescription)"))
        }
        proc.waitUntilExit()
        group.wait()

        if proc.terminationStatus != 0 {
            return (500, jsonError("clean-room script failed (exit \(proc.terminationStatus)): \(err.trimmingCharacters(in: .whitespacesAndNewlines))"))
        }

        let payload: [String: Any] = [
            "ok": true,
            "output": out.trimmingCharacters(in: .whitespacesAndNewlines),
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: payload),
              let str = String(data: data, encoding: .utf8) else {
            return (500, jsonError("serialization failed"))
        }
        return (200, str)
    }

    // MARK: - Helpers

    private func findTab(_ tabID: String) -> (PTYSessionManager, PTYSessionManager.Tab)? {
        for mgr in TerminalRegistry.shared.allManagers {
            if let tab = mgr.tabs.first(where: { $0.id == tabID }) {
                return (mgr, tab)
            }
        }
        return nil
    }

    private func jsonError(_ msg: String) -> String {
        let escaped = msg.replacingOccurrences(of: "\"", with: "\\\"")
        return #"{"error":"\#(escaped)"}"#
    }

    private func parseJSON(_ body: String) -> [String: Any]? {
        guard let data = body.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return nil
        }
        return obj
    }

    private func parseQuery(_ query: String) -> [String: String] {
        var result: [String: String] = [:]
        for pair in query.components(separatedBy: "&") {
            let kv = pair.components(separatedBy: "=")
            if kv.count == 2 {
                let k = kv[0].removingPercentEncoding ?? kv[0]
                let v = kv[1].removingPercentEncoding ?? kv[1]
                result[k] = v
            }
        }
        return result
    }

    private func sendResponse(_ conn: NWConnection, status: Int, body: String) {
        let statusText: String
        switch status {
        case 200: statusText = "OK"
        case 400: statusText = "Bad Request"
        case 404: statusText = "Not Found"
        case 500: statusText = "Internal Server Error"
        default:  statusText = "Unknown"
        }
        let bodyData = body.data(using: .utf8) ?? Data()
        let response = "HTTP/1.1 \(status) \(statusText)\r\n" +
            "Content-Type: application/json\r\n" +
            "Content-Length: \(bodyData.count)\r\n" +
            "Connection: close\r\n\r\n"
        var responseData = response.data(using: .utf8)!
        responseData.append(bodyData)

        conn.send(content: responseData, completion: .contentProcessed { _ in
            conn.cancel()
        })
    }
}
