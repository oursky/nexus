import Foundation
import Network
import OSLog

/// A minimal local HTTP/1.1 server that exposes headless control over terminal tabs.
/// Only active when the environment variable NEXUS_HEADLESS_RPC=1 is set.
///
/// Endpoints:
///   GET  /status
///   GET  /terminal/tabs
///   POST /terminal/open   { workspaceID, name? }
///   POST /terminal/write  { tabID, text }
///   GET  /terminal/read?tabID=...
///   POST /terminal/clear  { tabID }
@MainActor
public final class HeadlessRPCServer {
    private static let logger = Logger(subsystem: "com.nexus.NexusApp", category: "HeadlessRPCServer")
    public static let defaultPort: UInt16 = 7778

    private var listener: NWListener?
    private let port: UInt16
    private let clientProvider: () -> WebSocketDaemonClient?

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
