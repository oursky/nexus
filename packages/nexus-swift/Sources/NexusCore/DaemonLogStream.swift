import Foundation
import os

/// Subscribes to `daemon.logs.subscribe` over the active WebSocket client
/// and forwards each push entry to `os.Logger` under the DaemonLog category.
@MainActor
public final class DaemonLogStream {
    private static let logger = Logger(subsystem: "com.oursky.nexus", category: "DaemonLog")
    private var streamTask: Task<Void, Never>?
    private let client: WebSocketDaemonClient

    public init(client: WebSocketDaemonClient) {
        self.client = client
    }

    public func start() {
        guard streamTask == nil else { return }
        streamTask = Task { [weak self] in
            await self?.run()
        }
    }

    public func stop() {
        streamTask?.cancel()
        streamTask = nil
        client.setDaemonLogHandler(nil)
    }

    private func run() async {
        client.setDaemonLogHandler { params in
            let level = params["level"] as? String ?? "INFO"
            let msg   = params["msg"]   as? String ?? ""
            let attrs = params["attrs"] as? [String: Any] ?? [:]
            DaemonLogStream.logger.debug("[\(level, privacy: .public)] \(msg, privacy: .public) \(attrs.description, privacy: .public)")
        }

        do {
            _ = try await client.call("daemon.logs.subscribe")
        } catch {
            Self.logger.warning("daemon.logs.subscribe failed: \(error.localizedDescription, privacy: .public)")
        }

        await withTaskCancellationHandler {
            await Task.yield()
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(3600))
            }
        } onCancel: {
            // stop() clears the handler
        }
    }
}
