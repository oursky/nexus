import Foundation

/// Delegate that forwards URLSessionWebSocketTask open/close events to the owning client.
/// Held weakly by URLSession; owned by WebSocketDaemonClient.
private final class WebSocketDelegate: NSObject, URLSessionWebSocketDelegate, @unchecked Sendable {
    weak var client: WebSocketDaemonClient?

    func urlSession(
        _ session: URLSession,
        webSocketTask: URLSessionWebSocketTask,
        didOpenWithProtocol protocol: String?
    ) {
        client?.didOpen(task: webSocketTask)
    }

    func urlSession(
        _ session: URLSession,
        webSocketTask: URLSessionWebSocketTask,
        didCloseWith closeCode: URLSessionWebSocketTask.CloseCode,
        reason: Data?
    ) {
        client?.didClose(task: webSocketTask)
    }
}

/// Real client: JSON-RPC 2.0 over WebSocket, targeting the Nexus daemon.
///
/// Token resolution order:
///   1. NEXUS_DAEMON_TOKEN env var (for dev / CI)
///   2. macOS Keychain generic password (service configurable via
///      NEXUS_DAEMON_TOKEN_KEYCHAIN_SERVICE)
///
/// Auth: sends the token in the `Authorization: Bearer TOKEN` HTTP header on
/// the WebSocket upgrade request.
public final class WebSocketDaemonClient: DaemonClient, @unchecked Sendable {

    private let daemonURL: URL
    /// Optional pre-resolved token for remote profiles. When set, takes priority over env/keychain resolution.
    private let injectedToken: String?
    private let wsDelegate = WebSocketDelegate()
    /// Single session for all WebSocket connections — `URLSession()` per `connect()` leaked memory when
    /// refresh/load stacked during handshake (see AppState refresh loop).
    private let webSocketSession: URLSession
    private var task: URLSessionWebSocketTask?
    private var pending: [String: CheckedContinuation<Any, Error>] = [:]
    private var requestCounter = 0
    private let lock = NSLock()
    /// Serializes `connect()` / `task` mutation. Parallel `call()` (e.g. `async let` list RPCs) must not
    /// create overlapping WebSocket handshakes — that leaked tasks, stacked `receiveLoop`, and grew RAM.
    private let connectionLock = NSLock()
    /// Resolves when the WebSocket upgrade handshake completes (didOpen delegate callback).
    /// All `performRPC` callers await this before sending — prevents sending before the connection is live.
    private var readyTask: Task<Void, Error>?
    private var readyContinuation: CheckedContinuation<Void, Error>?
    /// Identity of the task whose open/close we are currently tracking.
    private var trackedTask: URLSessionWebSocketTask?

    public init(daemonURL: URL, token: String? = nil) {
        self.daemonURL = daemonURL
        self.injectedToken = token
        let cfg = URLSessionConfiguration.default
        cfg.timeoutIntervalForRequest = 90
        self.webSocketSession = URLSession(configuration: cfg, delegate: wsDelegate, delegateQueue: nil)
        wsDelegate.client = self
    }

    deinit {
        disconnect()
        // XCTest and rapid client churn otherwise retain URLSession delegate queues and buffers.
        webSocketSession.invalidateAndCancel()
    }

    // MARK: - Auth token

    public static func readToken() -> String {        if let env = ProcessInfo.processInfo.environment["NEXUS_DAEMON_TOKEN"], !env.isEmpty {
            return env
        }
        let configuredService = ProcessInfo.processInfo.environment["NEXUS_DAEMON_TOKEN_KEYCHAIN_SERVICE"]?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let services = [
            configuredService,
            "nexus-daemon-token",
            "nexus/token",
            "nexus-daemon",
            "nexus",
        ]
        for maybeService in services {
            guard let service = maybeService, !service.isEmpty else { continue }
            if let token = readMacKeychainPassword(service: service), !token.isEmpty {
                return token
            }
        }
        return ""
    }

    /// Reads a bearer token directly from a named Keychain service.
    /// Returns nil if not found. Used by remote profile token resolution.
    public static func readKeychainToken(service: String) -> String? {
        readMacKeychainPassword(service: service)
    }

    private static func readMacKeychainPassword(service: String) -> String? {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/bin/security")
        task.arguments = ["find-generic-password", "-s", service, "-w"]

        let out = Pipe()
        task.standardOutput = out
        task.standardError = Pipe()

        do {
            try task.run()
        } catch {
            return nil
        }
        task.waitUntilExit()
        guard task.terminationStatus == 0 else { return nil }

        let data = out.fileHandleForReading.readDataToEndOfFile()
        guard let raw = String(data: data, encoding: .utf8) else { return nil }
        let token = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return token.isEmpty ? nil : token
    }

    // MARK: - Connect / disconnect

    private func connect() async throws {
        connectionLock.lock()
        StartupTrace.checkpoint("ws.connect.enter", daemonURL.absoluteString)
        // Guard on task != nil, NOT task?.state == .running.
        // A freshly-resumed task is in state .suspended (connecting) — checking only .running
        // caused every concurrent call to create a new URLSessionWebSocketTask before the first
        // one finished its TCP handshake, leaking tasks+buffers and growing RAM unboundedly.
        if task != nil {
            let existingReady = readyTask
            connectionLock.unlock()
            StartupTrace.checkpoint("ws.connect.noop", "task already exists (state=\(task?.state.rawValue ?? -1))")
            // Wait for the in-progress handshake to complete before returning to caller.
            try await existingReady?.value
            return
        }
        let token = injectedToken ?? Self.readToken()
        var request = URLRequest(url: daemonURL)
        if !token.isEmpty {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        let newTask = webSocketSession.webSocketTask(with: request)
        task = newTask
        trackedTask = newTask

        // Create the ready Task while still holding connectionLock so that any concurrent connect()
        // that sees `task != nil` is guaranteed to also see a non-nil `readyTask`.  Previously
        // readyTask was assigned AFTER the unlock, creating a window where a concurrent caller could
        // capture `existingReady = nil`, await nil?.value (no-op), and proceed to call sock.send()
        // on a socket still mid-handshake.
        let ready: Task<Void, Error> = Task { [weak self] in
            guard let self else { throw CancellationError() }
            try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, Error>) in
                self.connectionLock.lock()
                // Continuation resolves via didOpen delegate callback after the WebSocket
                // upgrade handshake completes — not at resume() time.
                self.readyContinuation = cont
                newTask.resume()
                StartupTrace.checkpoint("ws.connect.resumed")
                self.connectionLock.unlock()
                self.receiveLoop()
            }
        }
        readyTask = ready
        connectionLock.unlock()
        try await ready.value
    }

    /// Called by WebSocketDelegate when the upgrade handshake succeeds for the tracked task.
    fileprivate func didOpen(task openedTask: URLSessionWebSocketTask) {
        connectionLock.lock()
        guard openedTask === trackedTask else {
            connectionLock.unlock()
            return
        }
        StartupTrace.checkpoint("ws.connect.open")
        let cont = readyContinuation
        readyContinuation = nil
        connectionLock.unlock()
        cont?.resume()
    }

    /// Called by WebSocketDelegate when the server closes the WebSocket for the tracked task.
    fileprivate func didClose(task closedTask: URLSessionWebSocketTask) {
        connectionLock.lock()
        guard closedTask === trackedTask else {
            connectionLock.unlock()
            return
        }
        StartupTrace.checkpoint("ws.connect.closed")
        let cont = readyContinuation
        readyContinuation = nil
        connectionLock.unlock()
        if let cont {
            cont.resume(throwing: RPCError(message: "WebSocket closed during handshake"))
        }
        failAll(RPCError(message: "WebSocket closed by server"))
    }

    public func disconnect() {
        // Same lock order as `failAll` (`lock` then `connectionLock`) to avoid deadlocks.
        lock.withLock {
            let all = pending
            pending.removeAll()
            for cont in all.values {
                cont.resume(throwing: CancellationError())
            }
        }
        connectionLock.lock()
        task?.cancel(with: .goingAway, reason: nil)
        task = nil
        trackedTask = nil
        readyTask = nil
        let cont = readyContinuation
        readyContinuation = nil
        connectionLock.unlock()
        cont?.resume(throwing: CancellationError())
    }

    // MARK: - Receive loop

    private func receiveLoop() {
        connectionLock.lock()
        let ws = task
        connectionLock.unlock()
        guard let ws else { return }
        ws.receive { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let msg):
                if case .string(let text) = msg { self.handle(text) }
                self.receiveLoop()
            case .failure(let err):
                // Signal failure so any pending connect() callers that haven't unblocked yet
                // (e.g. on immediate TCP rejection) unblock with an error.
                let nsErr = err as NSError
                print("[WS.receiveLoop] FAIL domain=\(nsErr.domain) code=\(nsErr.code) desc=\(nsErr.localizedDescription)")
                self.signalReady(err)
                self.failAll(err)
            }
        }
    }

    /// Resolves the one-shot readiness gate with an error (idempotent — only fires on first call).
    /// Only called from receiveLoop's failure path, in case the TCP connection is rejected before
    /// the ready signal fires (e.g. connect() resumes the task then receiveLoop gets an immediate error).
    private func signalReady(_ error: Error?) {
        connectionLock.lock()
        let cont = readyContinuation
        readyContinuation = nil
        connectionLock.unlock()
        guard let cont else { return }
        if let error {
            cont.resume(throwing: error)
        } else {
            cont.resume()
        }
    }

    private func handle(_ text: String) {
        guard let data = text.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }

        // Server push notification (no "id" field, has "method")
        if json["id"] == nil, let method = json["method"] as? String {
            let params = json["params"] as? [String: Any] ?? [:]
            handleNotification(method: method, params: params)
            return
        }

        // JSON-RPC response (has "id")
        guard let id = json["id"] as? String else { return }
        lock.withLock {
            guard let cont = pending.removeValue(forKey: id) else { return }
            if let errObj = json["error"] as? [String: Any],
               let msg    = errObj["message"] as? String {
                cont.resume(throwing: RPCError(message: msg))
            } else {
                cont.resume(returning: json["result"] as Any? ?? NSNull())
            }
        }
    }

    // MARK: - Notification dispatch (pty.data / pty.exit / daemon.log)

    private let ptyLock = NSLock()
    private var ptyDataHandlers: [String: @Sendable (String) -> Void] = [:]
    private var ptyExitHandlers: [String: @Sendable (Int) -> Void] = [:]
    // Buffers early pty.data that arrives before subscribePTY() is called (race window after pty.open)
    private var ptyDataBuffer: [String: [String]] = [:]

    private let notifLock = NSLock()
    private var daemonLogHandler: (@Sendable ([String: Any]) -> Void)?

    /// Register a handler for `daemon.log` push messages.
    public func setDaemonLogHandler(_ handler: (@Sendable ([String: Any]) -> Void)?) {
        notifLock.withLock { daemonLogHandler = handler }
    }

    private func handleNotification(method: String, params: [String: Any]) {
        switch method {
        case "pty.data":
            guard let sid  = params["sessionId"] as? String,
                  let data = params["data"]      as? String else { return }
            let h: (@Sendable (String) -> Void)? = ptyLock.withLock {
                if let handler = ptyDataHandlers[sid] { return handler }
                ptyDataBuffer[sid, default: []].append(data)
                return nil
            }
            h?(data)
        case "pty.exit":
            guard let sid = params["sessionId"] as? String else { return }
            let code = params["exitCode"] as? Int ?? -1
            let h = ptyLock.withLock { ptyExitHandlers[sid] }
            h?(code)
        case "daemon.log":
            let h = notifLock.withLock { daemonLogHandler }
            h?(params)
        default:
            break
        }
    }

    private func failAll(_ error: Error) {
        let isClean: Bool
        if let wsErr = error as? URLError, wsErr.code == .cancelled {
            isClean = true
        } else {
            isClean = false
        }
        if !isClean {
            print("[WebSocketClient] connection dropped unexpectedly: \(error)")
        }
        let all = lock.withLock { () -> [String: CheckedContinuation<Any, Error>] in
            let a = pending; pending.removeAll(); return a
        }
        connectionLock.lock()
        task?.cancel(with: .goingAway, reason: nil)
        task = nil
        trackedTask = nil
        readyTask = nil
        // readyContinuation is cleared by signalReady() (called before failAll from receiveLoop).
        // But guard here in case failAll is called from a path that skips signalReady.
        let leftoverCont = readyContinuation
        readyContinuation = nil
        connectionLock.unlock()
        leftoverCont?.resume(throwing: error)
        all.values.forEach { $0.resume(throwing: error) }
    }

    // MARK: - Low-level RPC

    /// Default ceiling for a single JSON-RPC round trip (connect + request + response).
    private static let defaultRPCSeconds: UInt64 = 45

    func call(_ method: String, params: [String: Any] = [:]) async throws -> Any {
        // Do NOT call disconnect() here on RPC errors — timeouts and transient failures must not tear
        // down the shared WebSocket for all concurrent calls. Socket-level teardown is handled by
        // failAll() in receiveLoop's .failure branch, which is the only place it is appropriate.
        do {
            return try await withTimeoutRPC(seconds: Self.defaultRPCSeconds) {
                try await self.performRPC(method: method, params: params)
            }
        } catch {
            let nsErr = error as NSError
            print("[WS.call] THREW method=\(method) domain=\(nsErr.domain) code=\(nsErr.code) desc=\(nsErr.localizedDescription)")
            throw error
        }
    }

    private func performRPC(method: String, params: [String: Any]) async throws -> Any {
        StartupTrace.rpc(method: method)
        try await connect()
        var id = ""
        lock.withLock {
            requestCounter += 1
            id = "req-\(requestCounter)"
        }
        let payload: [String: Any] = ["jsonrpc": "2.0", "id": id, "method": method, "params": params]
        let text = String(data: try JSONSerialization.data(withJSONObject: payload), encoding: .utf8)!
        // withTaskCancellationHandler ensures that if the enclosing task is cancelled (e.g. by
        // withTimeoutRPC's group.cancelAll()), the pending continuation is removed from the dict
        // immediately. Without this, the cancelled task's continuation stays in pending[] and is
        // double-resumed when disconnect()/failAll() later iterates the dict — undefined behaviour.
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { cont in
                lock.withLock { pending[id] = cont }
                connectionLock.lock()
                let sock = task
                connectionLock.unlock()
                guard let sock else {
                    _ = lock.withLock { pending.removeValue(forKey: id) }
                    cont.resume(throwing: RPCError(message: "WebSocket task is nil"))
                    return
                }
                sock.send(.string(text)) { [weak self] err in
                    guard let self else { return }
                    if let err {
                        let nsErr = err as NSError
                        print("[WS.send] FAIL method=\(method) id=\(id) domain=\(nsErr.domain) code=\(nsErr.code) desc=\(nsErr.localizedDescription)")
                        if self.isConnectionFatalSendError(err) {
                            // Transport-level send failure means the shared socket is no longer
                            // trustworthy. Tear down shared state so the next RPC reconnects.
                            self.failAll(err)
                        } else {
                            let cont2 = self.lock.withLock { self.pending.removeValue(forKey: id) }
                            cont2?.resume(throwing: err)
                        }
                    }
                }
            }
        } onCancel: { [weak self] in
            guard let self else { return }
            let cont = self.lock.withLock { self.pending.removeValue(forKey: id) }
            cont?.resume(throwing: CancellationError())
        }
    }

    private func isConnectionFatalSendError(_ error: Error) -> Bool {
        if error is CancellationError { return false }
        if let urlErr = error as? URLError, urlErr.code == .cancelled { return false }
        return true
    }

    private func withTimeoutRPC(seconds: UInt64, _ work: @escaping () async throws -> Any) async throws -> Any {
        try await withThrowingTaskGroup(of: Any.self) { group in
            group.addTask { try await work() }
            group.addTask {
                try await Task.sleep(nanoseconds: seconds * 1_000_000_000)
                throw RPCError(message: "timed out after \(seconds)s (no daemon response)")
            }
            defer { group.cancelAll() }
            return try await group.next()!
        }
    }

    // MARK: - DaemonClient

    public func listWorkspaces() async throws -> [Workspace] {
        let result = try await call("workspace.list")
        guard let dict = result as? [String: Any], let arr = dict["workspaces"] as? [Any] else { return [] }
        return try JSONDecoder().decode([Workspace].self,
                                       from: JSONSerialization.data(withJSONObject: arr))
    }

    public func listProjects() async throws -> [Project] {
        let result = try await call("project.list")
        guard let dict = result as? [String: Any], let arr = dict["projects"] as? [Any] else { return [] }
        return try JSONDecoder().decode([Project].self,
                                        from: JSONSerialization.data(withJSONObject: arr))
    }

    public func createProject(repo: String) async throws -> Project {
        let trimmedRepo = repo.trimmingCharacters(in: .whitespacesAndNewlines)
        let projectName = try inferProjectName(from: trimmedRepo)
        let result = try await call("project.create", params: [
            "name": projectName,
            "repoUrl": trimmedRepo,
        ])
        guard let dict = result as? [String: Any], let raw = dict["project"] else {
            throw RPCError(message: "unexpected response from project.create")
        }
        return try JSONDecoder().decode(Project.self,
                                        from: JSONSerialization.data(withJSONObject: raw))
    }

    public func removeProject(id: String) async throws {
        _ = try await call("project.remove", params: ["id": id])
    }

    private func inferProjectName(from repo: String) throws -> String {
        var name = repo
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "/"))

        if let slash = name.lastIndex(of: "/") {
            name = String(name[name.index(after: slash)...])
        }
        if let colon = name.lastIndex(of: ":") {
            name = String(name[name.index(after: colon)...])
        }
        if name.hasSuffix(".git") {
            name = String(name.dropLast(4))
        }
        name = name.trimmingCharacters(in: .whitespacesAndNewlines)

        if name.isEmpty {
            throw RPCError(message: "project name is required")
        }
        return name
    }

    public func createWorkspace(spec: WorkspaceCreateSpec) async throws -> Workspace {
        let project = try await createProject(repo: spec.repo)
        let trimmed = spec.repo.trimmingCharacters(in: .whitespacesAndNewlines)
        return try await createWorkspaceDaemon(spec: WorkspaceDaemonCreateSpec(
            repo: trimmed,
            ref: spec.ref,
            workspaceName: spec.workspaceName,
            agentProfile: spec.agentProfile,
            backend: spec.backend,
            projectId: project.id
        ))
    }

    private func looksLikeRemoteGitURL(_ repo: String) -> Bool {
        let t = repo.trimmingCharacters(in: .whitespaces)
        if t.hasPrefix("git@") { return true }
        if let u = URL(string: t), u.scheme != nil, u.host != nil { return true }
        return false
    }

    public func createWorkspaceDaemon(spec: WorkspaceDaemonCreateSpec) async throws -> Workspace {
        let specDict = try spec.asDictionary()
        let result = try await call("workspace.create", params: ["spec": specDict])
        guard let dict = result as? [String: Any], let ws = dict["workspace"] else {
            throw RPCError(message: "unexpected response from workspace.create")
        }
        return try JSONDecoder().decode(Workspace.self,
                                        from: JSONSerialization.data(withJSONObject: ws))
    }

    public func forkWorkspace(parentID: String, childName: String, childRef: String) async throws -> Workspace {
        let result = try await call("workspace.fork", params: [
            "id": parentID,
            "childWorkspaceName": childName,
            "childRef": childRef,
        ])
        guard let dict = result as? [String: Any], let ws = dict["workspace"] else {
            throw RPCError(message: "unexpected response from workspace.fork")
        }
        return try JSONDecoder().decode(Workspace.self,
                                        from: JSONSerialization.data(withJSONObject: ws))
    }

    public func discoverPorts(workspaceID: String) async throws -> [[String: Any]] {
        let result = try await call("workspace.discover-ports", params: ["id": workspaceID])
        if let arr = result as? [[String: Any]] { return arr }
        if let arr = result as? [Any] {
            return arr.compactMap { $0 as? [String: Any] }
        }
        return []
    }

    public func spotlightStart(workspaceId: String, localPort: Int, remotePort: Int, protocolText: String?) async throws -> (targetHost: String, targetPort: Int) {
        var spec: [String: Any] = [
            "workspaceId": workspaceId,
            "localPort": localPort,
            "remotePort": remotePort,
        ]
        if let p = protocolText, !p.isEmpty { spec["protocol"] = p }
        let result = try await call("spotlight.start", params: [
            "workspaceId": workspaceId,
            "spec": spec,
        ])
        guard let dict = result as? [String: Any], let fwd = dict["forward"] as? [String: Any] else {
            return ("127.0.0.1", remotePort)
        }
        let th = fwd["targetHost"] as? String ?? "127.0.0.1"
        let tp = (fwd["remotePort"] as? Int) ?? (fwd["remotePort"] as? NSNumber)?.intValue ?? remotePort
        return (th, max(1, tp))
    }

    public func spotlightStopWorkspace(workspaceId: String) async throws {
        _ = try await call("spotlight.stop", params: ["workspaceId": workspaceId])
    }

    public func startWorkspace(id: String) async throws {
        _ = try await call("workspace.start", params: ["id": id])
    }

    public func stopWorkspace(id: String) async throws {
        _ = try await call("workspace.stop", params: ["id": id])
    }

    public func removeWorkspace(id: String) async throws {
        _ = try await call("workspace.remove", params: ["id": id])
    }

    public func markWorkspaceReady(id: String) async throws {
        _ = try await call("workspace.ready", params: ["id": id])
    }

    public func listPorts(workspaceId: String) async throws -> [ForwardedPort] {
        let result = try await call("workspace.ports.list", params: ["workspaceId": workspaceId])
        guard let dict = result as? [String: Any] else { return [] }
        return parseSpotlightForwards(from: dict["forwards"])
    }

    public func addPortForward(workspaceId: String, localPort: Int, remotePort: Int) async throws {
        let spec: [String: Any] = [
            "workspaceId": workspaceId,
            "localPort": localPort,
            "remotePort": remotePort,
        ]
        _ = try await call("workspace.ports.add", params: [
            "workspaceId": workspaceId,
            "spec": spec,
        ])
    }

    public func removePortForward(workspaceId: String, forwardId: String) async throws {
        _ = try await call("workspace.ports.remove", params: [
            "workspaceId": workspaceId,
            "forwardId": forwardId,
        ])
    }

    public func startTunnels(workspaceId: String) async throws -> TunnelStatus {
        let ports = try await discoverPorts(workspaceID: workspaceId)
        if ports.isEmpty {
            return TunnelStatus(active: false, activeWorkspaceId: "")
        }
        for p in ports {
            let lp = (p["localPort"] as? Int) ?? (p["localPort"] as? NSNumber)?.intValue ?? 0
            let rp = (p["remotePort"] as? Int) ?? (p["remotePort"] as? NSNumber)?.intValue ?? 0
            guard lp > 0, rp > 0 else { continue }
            let proto = p["protocol"] as? String
            _ = try await spotlightStart(workspaceId: workspaceId, localPort: lp, remotePort: rp, protocolText: proto)
        }
        return TunnelStatus(active: true, activeWorkspaceId: workspaceId)
    }

    public func stopTunnels(workspaceId: String) async throws -> TunnelStatus {
        try await spotlightStopWorkspace(workspaceId: workspaceId)
        return TunnelStatus(active: false, activeWorkspaceId: "")
    }

    public func tunnelStatus(workspaceId: String) async throws -> TunnelStatus {
        let ports = try await listPorts(workspaceId: workspaceId)
        let active = ports.contains { $0.tunneled }
        return TunnelStatus(active: active, activeWorkspaceId: active ? workspaceId : "")
    }

    public func workspaceInfo(id: String) async throws -> WorkspaceInfo {
        let result = try await call("workspace.info", params: ["id": id])
        guard let dict = result as? [String: Any],
              let ws = dict["workspace"] as? [String: Any] else {
            throw RPCError(message: "unexpected workspace.info response")
        }
        let wsPath = ws["rootPath"] as? String ?? ws["repo"] as? String ?? ""
        let wsId = ws["id"] as? String ?? id
        return WorkspaceInfo(workspaceId: wsId, workspacePath: wsPath, ports: [])
    }

    public func getDaemonSandboxResourceSettings() async throws -> SandboxResourceSettings {
        SandboxResourceSettings(defaultMemoryMiB: 1024, defaultVCPUs: 1, maxMemoryMiB: 4096, maxVCPUs: 4)
    }

    public func updateDaemonSandboxResourceSettings(_ settings: SandboxResourceSettings) async throws -> SandboxResourceSettings {
        settings
    }

    public func checkVMSSH(workspaceId: String) async throws -> VMSSHCheckResult {
        let result = try await call("workspace.sshcheck", params: ["id": workspaceId])
        guard let dict = result as? [String: Any] else {
            throw RPCError(message: "unexpected workspace.sshcheck response")
        }
        let ok = dict["ok"] as? Bool ?? false
        let guestIP = dict["guestIp"] as? String ?? ""
        let whoami = dict["whoami"] as? String ?? ""
        let error = dict["error"] as? String ?? ""
        let stderr = dict["stderr"] as? String ?? ""
        return VMSSHCheckResult(ok: ok, guestIP: guestIP, whoami: whoami, error: error, stderr: stderr)
    }

    private func parseSpotlightForwards(from raw: Any?) -> [ForwardedPort] {
        guard let items = raw as? [[String: Any]] else { return [] }
        return items.compactMap { item in
            let lp = (item["localPort"] as? Int) ?? (item["localPort"] as? NSNumber)?.intValue
            guard let local = lp, local > 0 else { return nil }
            let rp = (item["remotePort"] as? Int) ?? (item["remotePort"] as? NSNumber)?.intValue ?? local
            let state = (item["state"] as? String) ?? ""
            let tunneled = (state == "active")
            let fid = item["id"] as? String
            return ForwardedPort(
                id: local,
                remotePort: rp,
                preferred: (item["source"] as? String) == "user",
                tunneled: tunneled,
                process: nil,
                forwardId: fid
            )
        }
    }

    private func parseForwardedPorts(from raw: Any?) -> [ForwardedPort] {
        parseSpotlightForwards(from: raw)
    }

    private func parseTunnelStatus(dict: [String: Any]) -> TunnelStatus {
        TunnelStatus(
            active: dict["active"] as? Bool ?? false,
            activeWorkspaceId: dict["activeWorkspaceId"] as? String ?? ""
        )
    }

    private func parseSandboxResourceSettings(_ dict: [String: Any]) -> SandboxResourceSettings {
        let defaultMemoryMiB = (dict["defaultMemoryMiB"] as? NSNumber)?.intValue ?? 1024
        let defaultVCPUs = (dict["defaultVCPUs"] as? NSNumber)?.intValue ?? 1
        let maxMemoryMiB = (dict["maxMemoryMiB"] as? NSNumber)?.intValue ?? 4096
        let maxVCPUs = (dict["maxVCPUs"] as? NSNumber)?.intValue ?? 4
        return SandboxResourceSettings(
            defaultMemoryMiB: defaultMemoryMiB,
            defaultVCPUs: defaultVCPUs,
            maxMemoryMiB: maxMemoryMiB,
            maxVCPUs: maxVCPUs
        )
    }
    // MARK: - PTY

    /// Opens a PTY session in the workspace. Returns the session ID (`pty.create`).
    public func openPTY(workspaceId: String, cols: Int, rows: Int, useTmux: Bool = false) async throws -> String {
        try await openPTY(workspaceId: workspaceId, name: "Terminal", cols: cols, rows: rows, useTmux: useTmux)
    }

    public func writePTY(sessionId: String, data: String) async throws {
        _ = try await call("pty.write", params: ["sessionId": sessionId, "data": data])
    }

    public func resizePTY(sessionId: String, cols: Int, rows: Int) async throws {
        _ = try await call("pty.resize", params: ["sessionId": sessionId, "cols": cols, "rows": rows])
    }

    public func closePTY(sessionId: String) async throws {
        _ = try await call("pty.close", params: ["sessionId": sessionId])
    }

    /// Re-attaches an existing PTY session to the current WebSocket connection so
    /// that pty.data notifications reach this client.  Called when the app reconnects
    /// and finds sessions that were live on a previous connection.
    public func attachPTY(sessionId: String) async throws -> Bool {
        _ = try await call("pty.reattach", params: ["sessionId": sessionId])
        return true
    }

    /// Register callbacks for output and exit events on a PTY session.
    /// Drains any early-buffered pty.data that arrived before this call.
    public func subscribePTY(
        sessionId: String,
        onData: @escaping @Sendable (String) -> Void,
        onExit: @escaping @Sendable (Int) -> Void
    ) {
        let buffered: [String] = ptyLock.withLock {
            ptyDataHandlers[sessionId] = onData
            ptyExitHandlers[sessionId] = onExit
            return ptyDataBuffer.removeValue(forKey: sessionId) ?? []
        }
        for chunk in buffered { onData(chunk) }
    }

    public func unsubscribePTY(sessionId: String) {
        ptyLock.withLock {
            ptyDataHandlers.removeValue(forKey: sessionId)
            ptyExitHandlers.removeValue(forKey: sessionId)
            ptyDataBuffer.removeValue(forKey: sessionId)
        }
    }

    // MARK: - Multi-tab PTY Session Management

    /// Opens a PTY session with a custom name for tab display (`pty.create`).
    public func openPTY(workspaceId: String, name: String, cols: Int, rows: Int, useTmux: Bool = false) async throws -> String {
        _ = useTmux
        let result = try await call("pty.create", params: [
            "workspaceId": workspaceId,
            "name": name,
            "shell": "bash",
            "args": ["-l"],
            "workDir": "",
            "cols": cols,
            "rows": rows,
        ])
        guard let dict = result as? [String: Any],
              let sid = dict["id"] as? String else {
            throw RPCError(message: "unexpected pty.create response")
        }
        return sid
    }

    /// Lists all PTY sessions for a workspace
    public func listPTYSessions(workspaceId: String) async throws -> [PTYSessionInfo] {
        let result = try await call("pty.list", params: ["workspaceId": workspaceId])
        guard let dict = result as? [String: Any],
              let sessions = dict["sessions"] as? [[String: Any]] else {
            return []
        }
        return sessions.compactMap { PTYSessionInfo(from: $0) }
    }

    /// Gets info for a specific PTY session
    public func getPTYSession(sessionId: String) async throws -> PTYSessionInfo {
        let result = try await call("pty.get", params: ["sessionId": sessionId])
        guard let dict = result as? [String: Any],
              let session = dict["session"] as? [String: Any],
              let info = PTYSessionInfo(from: session) else {
            throw RPCError(message: "session not found")
        }
        return info
    }

    /// Renames a PTY session (updates tab name)
    public func renamePTYSession(sessionId: String, name: String) async throws -> Bool {
        let result = try await call("pty.rename", params: [
            "sessionId": sessionId,
            "name": name,
        ])
        guard let dict = result as? [String: Any] else { return false }
        return dict["success"] as? Bool ?? false
    }

    // MARK: - Tmux Support

    /// Executes a tmux command on a tmux-based session
    public func tmuxCommand(sessionId: String, command: String, args: [String] = []) async throws -> TmuxCommandResult {
        let result = try await call("pty.tmux", params: [
            "sessionId": sessionId,
            "command": command,
            "args": args,
        ])
        guard let dict = result as? [String: Any] else {
            throw RPCError(message: "unexpected tmux response")
        }
        return TmuxCommandResult(
            success: dict["success"] as? Bool ?? false,
            output: dict["output"] as? String,
            error: dict["error"] as? String
        )
    }
}

// MARK: - PTY Session Info

public struct PTYSessionInfo: Identifiable, Sendable {
    public let id: String
    public let workspaceId: String
    public let name: String
    public let shell: String
    public let workDir: String
    public let cols: Int
    public let rows: Int
    public let createdAt: String
    public let isRemote: Bool
    public let isTmux: Bool
    public let tmuxSession: String?

    public init?(from dict: [String: Any]) {
        guard let id = dict["id"] as? String,
              let workspaceId = dict["workspaceId"] as? String,
              let name = dict["name"] as? String else { return nil }
        self.id = id
        self.workspaceId = workspaceId
        self.name = name
        self.shell = dict["shell"] as? String ?? "bash"
        self.workDir = dict["workDir"] as? String ?? "/workspace"
        self.cols = dict["cols"] as? Int ?? 80
        self.rows = dict["rows"] as? Int ?? 24
        self.createdAt = dict["createdAt"] as? String ?? ""
        self.isRemote = dict["isRemote"] as? Bool ?? false
        self.isTmux = dict["isTmux"] as? Bool ?? false
        self.tmuxSession = dict["tmuxSession"] as? String
    }
}

// MARK: - Tmux Command Result

public struct TmuxCommandResult: Sendable {
    public let success: Bool
    public let output: String?
    public let error: String?
}

// MARK: - Error type

public struct RPCError: Error, LocalizedError {
    public let message: String
    public var errorDescription: String? { message }
    public init(message: String) { self.message = message }
}

// MARK: - Workspace info (from workspace.info RPC)

public struct WorkspaceInfo: Sendable {
    public let workspaceId: String
    public let workspacePath: String
    public let ports: [ForwardedPort]

    public init(workspaceId: String, workspacePath: String, ports: [ForwardedPort]) {
        self.workspaceId   = workspaceId
        self.workspacePath = workspacePath
        self.ports         = ports
    }
}

// MARK: - Version info (unauthenticated HTTP endpoint)

/// Version and protocol information returned by the daemon's `/version` endpoint.
public struct DaemonInfo: Decodable, Equatable {
    public let name: String
    public let version: String
    public let commit: String
    public let builtAt: String
    public let protocolVersion: Int

    public init(name: String, version: String, commit: String, builtAt: String, protocolVersion: Int) {
        self.name = name
        self.version = version
        self.commit = commit
        self.builtAt = builtAt
        self.protocolVersion = protocolVersion
    }

    /// Protocol version this build of the Swift app requires.
    /// Must match `ProtocolVersion` in `packages/nexus/pkg/buildinfo/buildinfo.go`.
    public static let requiredProtocol = 2

    /// Dev builds (go run / go build without ldflags) always report "0.0.0-dev".
    /// Treat them as always compatible so local development is never blocked.
    public var isCompatible: Bool {
        version == "0.0.0-dev" || protocolVersion >= Self.requiredProtocol
    }

    enum CodingKeys: String, CodingKey {
        case name, version, commit, builtAt
        case protocolVersion = "protocol"
    }
}

extension WebSocketDaemonClient {
    /// Fetches `/version` over plain HTTP (no auth).
    /// Returns `nil` if the daemon is unreachable or the response can't be decoded.
    public func fetchDaemonInfo() async -> DaemonInfo? {
        guard let host = daemonURL.host,
              let port = daemonURL.port else {
            StartupTrace.checkpoint("http.version.skip", "bad daemonURL host/port")
            return nil
        }
        let scheme = daemonURL.scheme == "wss" ? "https" : "http"
        guard let url = URL(string: "\(scheme)://\(host):\(port)/version") else {
            StartupTrace.checkpoint("http.version.skip", "bad version URL")
            return nil
        }
        StartupTrace.checkpoint("http.version.req", url.absoluteString)
        var req = URLRequest(url: url)
        req.timeoutInterval = 2
        guard let (data, _) = try? await URLSession.shared.data(for: req) else {
            StartupTrace.checkpoint("http.version.fail", "no response")
            return nil
        }
        guard let info = try? JSONDecoder().decode(DaemonInfo.self, from: data) else {
            StartupTrace.checkpoint("http.version.fail", "decode")
            return nil
        }
        StartupTrace.checkpoint("http.version.ok", "v=\(info.version) protocol=\(info.protocolVersion)")
        return info
    }
}
