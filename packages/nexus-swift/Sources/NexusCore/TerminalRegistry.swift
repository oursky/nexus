import Foundation
import OSLog

/// A registry that maps workspace IDs to their active PTYSessionManager instances.
/// This decouples the session managers from the SwiftUI view layer so that the
/// headless HTTP RPC server can find and interact with terminals regardless of
/// which view is currently focused.
///
/// Thread safety: all mutations happen on @MainActor (same as PTYSessionManager).
@MainActor
public final class TerminalRegistry {
    public static let shared = TerminalRegistry()
    private static let logger = Logger(subsystem: "com.oursky.nexus", category: "TerminalRegistry")

    /// Output ring-buffer for each PTY session ID.
    /// Populated by TabTerminalView.Coordinator when it feeds data to SwiftTerm.
    private var outputBuffers: [String: OutputBuffer] = [:]

    /// Active PTYSessionManagers keyed by workspace ID.
    private var managers: [String: PTYSessionManager] = [:]

    private init() {}

    // MARK: - Manager registration

    public func register(_ manager: PTYSessionManager) {
        managers[manager.workspaceId] = manager
        Self.logger.debug("terminal.registry register workspaceId=\(manager.workspaceId, privacy: .public)")
    }

    public func unregister(workspaceId: String) {
        managers.removeValue(forKey: workspaceId)
        Self.logger.debug("terminal.registry unregister workspaceId=\(workspaceId, privacy: .public)")
    }

    public func manager(for workspaceId: String) -> PTYSessionManager? {
        managers[workspaceId]
    }

    /// Returns an existing manager for the workspace, or creates one using
    /// the same PTYSessionManager path used by the SwiftUI view model.
    @discardableResult
    public func ensureManager(workspaceId: String, client: WebSocketDaemonClient) -> PTYSessionManager {
        if let existing = managers[workspaceId] {
            return existing
        }
        let manager = PTYSessionManager(workspaceId: workspaceId, client: client)
        managers[workspaceId] = manager
        manager.startRefreshLoop()
        Self.logger.debug("terminal.registry ensure workspaceId=\(workspaceId, privacy: .public) created=true")
        return manager
    }

    public var allManagers: [PTYSessionManager] {
        Array(managers.values)
    }

    /// Clears all registered managers. Call on daemon disconnect or before
    /// connecting to a new daemon to prevent stale managers from using a dead client.
    public func reset() {
        managers.removeAll()
        outputBuffers.removeAll()
        Self.logger.debug("terminal.registry reset_all")
    }

    // MARK: - Output buffer management

    /// Called by TabTerminalView.Coordinator each time PTY data arrives.
    public func appendOutput(_ text: String, for sessionId: String) {
        if outputBuffers[sessionId] == nil {
            outputBuffers[sessionId] = OutputBuffer()
        }
        outputBuffers[sessionId]!.append(text)
    }

    /// Drains and returns buffered output for a session. Returns nil if no buffer.
    public func drainOutput(for sessionId: String) -> String? {
        outputBuffers[sessionId]?.drain()
    }

    /// Clears the output buffer for a session.
    public func clearOutput(for sessionId: String) {
        outputBuffers[sessionId]?.clear()
    }

    public func removeBuffer(for sessionId: String) {
        outputBuffers.removeValue(forKey: sessionId)
    }
}

// MARK: - Output Buffer

/// A simple ring buffer that stores up to `capacity` bytes of PTY output.
final class OutputBuffer {
    private static let capacity = 65_536  // 64KB
    private var data = ""

    func append(_ text: String) {
        data.append(text)
        if data.utf8.count > OutputBuffer.capacity {
            // Drop from the front
            let excess = data.utf8.count - OutputBuffer.capacity
            let dropIndex = data.utf8.index(data.startIndex, offsetBy: excess, limitedBy: data.endIndex) ?? data.endIndex
            data = String(data[dropIndex...])
        }
    }

    /// Returns all buffered data and resets the buffer.
    func drain() -> String {
        let result = data
        data = ""
        return result
    }

    func clear() {
        data = ""
    }

    var isEmpty: Bool { data.isEmpty }
}
