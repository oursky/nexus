import Foundation
import Combine
import OSLog

/// Manages multiple PTY sessions (tabs) for a workspace
@MainActor
public final class PTYSessionManager: ObservableObject {
    private static let logger = Logger(subsystem: "com.oursky.nexus", category: "PTYSessionManager")

    /// A single terminal tab
    public struct Tab: Identifiable, Equatable {
        public let id: String
        public var name: String
        public var isActive: Bool
        public var isLoading: Bool
        public var error: String?

        public init(id: String, name: String, isActive: Bool = false, isLoading: Bool = true, error: String? = nil) {
            self.id = id
            self.name = name
            self.isActive = isActive
            self.isLoading = isLoading
            self.error = error
        }

        public static func == (lhs: Tab, rhs: Tab) -> Bool {
            lhs.id == rhs.id &&
            lhs.name == rhs.name &&
            lhs.isActive == rhs.isActive &&
            lhs.isLoading == rhs.isLoading &&
            lhs.error == rhs.error
        }
    }

    @Published public private(set) var tabs: [Tab] = []
    @Published public var activeTabId: String? {
        didSet {
            updateActiveState()
        }
    }

    public let workspaceId: String
    private let client: WebSocketDaemonClient
    private var refreshTask: Task<Void, Never>?
    /// Guard against concurrent createTab calls (e.g. from SwiftUI re-evaluating onAppear).
    private var isCreatingTab = false

    public init(workspaceId: String, client: WebSocketDaemonClient) {
        self.workspaceId = workspaceId
        self.client = client
    }

    deinit {
        refreshTask?.cancel()
    }

    // MARK: - Tab Management

    /// Create a new terminal tab
    public func createTab(name: String? = nil) async {
        let t0 = Date()
        // Prevent concurrent creation (e.g. SwiftUI calling onAppear multiple times).
        guard !isCreatingTab else {
            Self.logger.warning("pty.tab.create SKIPPED-BUSY workspace=\(self.workspaceId, privacy: .public) existing_tabs=\(self.tabs.count, privacy: .public)")
            return
        }
        isCreatingTab = true
        defer { isCreatingTab = false }
        let tabName = name ?? "Tab \(tabs.count + 1)"
        let tempId = UUID().uuidString
        let startedAt = Date()
        let loadingTimeoutSeconds: UInt64 = 20

        let timeoutTask = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: loadingTimeoutSeconds * 1_000_000_000)
            guard let self else { return }
            guard let index = self.tabs.firstIndex(where: { $0.id == tempId }), self.tabs[index].isLoading else {
                return
            }
            Self.logger.error("pty.tab.create UI-TIMEOUT workspace=\(self.workspaceId, privacy: .public) tab=\(tabName, privacy: .public)")
            self.tabs[index].isLoading = false
            self.tabs[index].error = "Timed out opening terminal (\(loadingTimeoutSeconds)s)."
        }
        defer { timeoutTask.cancel() }

        Self.logger.notice("pty.tab.create ENTER workspace=\(self.workspaceId, privacy: .public) tab=\(tabName, privacy: .public) guard_wait_ms=\(Int(Date().timeIntervalSince(t0)*1000), privacy: .public)")

        // Add loading tab
        await MainActor.run {
            let newTab = Tab(id: tempId, name: tabName, isLoading: true)
            tabs.append(newTab)
            activeTabId = tempId
        }

        do {
            // Calculate default terminal size
            let cols = 120
            let rows = 40

            let sessionId = try await openPTYWithReadinessRetries(
                workspaceId: self.workspaceId,
                tabName: tabName,
                cols: cols,
                rows: rows,
                startedAt: startedAt
            )

            let elapsedMs = Int(Date().timeIntervalSince(startedAt) * 1000)
            Self.logger.notice("pty.tab.create SUCCESS workspace=\(self.workspaceId, privacy: .public) tab=\(tabName, privacy: .public) session=\(sessionId, privacy: .public) total_ms=\(elapsedMs, privacy: .public)")

            // Update with real session ID
            await MainActor.run {
                if let index = tabs.firstIndex(where: { $0.id == tempId }) {
                    tabs[index] = Tab(
                        id: sessionId,
                        name: tabName,
                        isActive: true,
                        isLoading: false
                    )
                    activeTabId = sessionId
                }
            }

            // Subscribe to PTY events
            setupPTYHandlers(sessionId: sessionId)

        } catch {
            let elapsedMs = Int(Date().timeIntervalSince(startedAt) * 1000)
            if case let AsyncDeadlineError.exceeded(seconds) = error {
                Self.logger.error("pty.tab.create TIMEOUT workspace=\(self.workspaceId, privacy: .public) tab=\(tabName, privacy: .public) timeout_s=\(seconds, privacy: .public) elapsed_ms=\(elapsedMs, privacy: .public)")
            } else {
                Self.logger.error("pty.tab.create FAILED workspace=\(self.workspaceId, privacy: .public) tab=\(tabName, privacy: .public) elapsed_ms=\(elapsedMs, privacy: .public) error=\(error.localizedDescription, privacy: .public)")
            }
            await MainActor.run {
                if let index = tabs.firstIndex(where: { $0.id == tempId }) {
                    tabs[index].isLoading = false
                    if case let AsyncDeadlineError.exceeded(seconds) = error {
                        tabs[index].error = "Timed out opening terminal (\(seconds)s)."
                    } else {
                        tabs[index].error = error.localizedDescription
                    }
                }
            }
        }
    }

    private func openPTYWithReadinessRetries(
        workspaceId: String,
        tabName: String,
        cols: Int,
        rows: Int,
        startedAt: Date
    ) async throws -> String {
        let maxAttempts = 3
        var lastError: Error?
        let tOpenPTY = Date()

        for attempt in 1...maxAttempts {
            Self.logger.notice("pty.tab.create CALLING-openPTY workspace=\(workspaceId, privacy: .public) tab=\(tabName, privacy: .public) attempt=\(attempt, privacy: .public) ms_since_enter=\(Int(Date().timeIntervalSince(startedAt)*1000), privacy: .public)")
            do {
                let sessionId = try await AsyncDeadline.withSeconds(20) {
                    try await self.client.openPTY(
                        workspaceId: workspaceId,
                        name: tabName,
                        cols: cols,
                        rows: rows,
                        useTmux: true
                    )
                }
                let openPTYMs = Int(Date().timeIntervalSince(tOpenPTY) * 1000)
                Self.logger.notice("pty.tab.create OPEN-PTY-OK workspace=\(workspaceId, privacy: .public) tab=\(tabName, privacy: .public) attempt=\(attempt, privacy: .public) openPTY_ms=\(openPTYMs, privacy: .public)")
                return sessionId
            } catch {
                lastError = error
                let msg = error.localizedDescription.lowercased()
                let retryable = msg.contains("not ready") || msg.contains("target is busy") || msg.contains("starting")
                Self.logger.warning("pty.tab.create OPEN-PTY-FAILED workspace=\(workspaceId, privacy: .public) tab=\(tabName, privacy: .public) attempt=\(attempt, privacy: .public) retryable=\(retryable, privacy: .public) error=\(error.localizedDescription, privacy: .public)")
                guard retryable, attempt < maxAttempts else { throw error }
                try? await self.client.markWorkspaceReady(id: workspaceId)
                try? await Task.sleep(nanoseconds: UInt64(attempt) * 500_000_000)
            }
        }
        throw lastError ?? RPCError(message: "openPTY failed")
    }

    /// Close a specific tab
    public func closeTab(id: String) async {
        // Close PTY session
        try? await client.closePTY(sessionId: id)

        // Remove from tabs
        await MainActor.run {
            tabs.removeAll { $0.id == id }

            // Select another tab if needed
            if activeTabId == id {
                activeTabId = tabs.first?.id
            }
        }
    }

    /// Rename a tab
    public func renameTab(id: String, to newName: String) async {
        let success = try? await client.renamePTYSession(sessionId: id, name: newName)

        if success == true {
            await MainActor.run {
                if let index = tabs.firstIndex(where: { $0.id == id }) {
                    tabs[index].name = newName
                }
            }
        }
    }

    /// Write text input to a PTY session (used by headless RPC).
    public func writePTY(sessionId: String, text: String) async throws {
        try await client.writePTY(sessionId: sessionId, data: text)
    }

    /// Switch to a different tab
    public func switchToTab(id: String) {
        activeTabId = id
    }

    /// Refresh tabs from server (sync state)
    public func refreshTabs() async {
        do {
            let sessions = try await client.listPTYSessions(workspaceId: workspaceId)

            await MainActor.run {
                // Update existing tabs and add new ones
                var newTabs: [Tab] = []
                for session in sessions {
                    if let existing = tabs.first(where: { $0.id == session.id }) {
                        // Keep existing tab but update name if changed
                        newTabs.append(Tab(
                            id: session.id,
                            name: session.name,
                            isActive: session.id == activeTabId,
                            isLoading: false,
                            error: existing.error
                        ))
                    } else {
                        // New tab from server
                        newTabs.append(Tab(
                            id: session.id,
                            name: session.name,
                            isActive: false,
                            isLoading: false
                        ))
                    }
                }
                self.tabs = newTabs

                // Set active tab if none selected
                if activeTabId == nil || !tabs.contains(where: { $0.id == activeTabId }) {
                    activeTabId = tabs.first?.id
                }
            }
        } catch {
            // Silent fail - tabs will sync next time
        }
    }

    /// Start auto-refresh (call when view appears)
    public func startRefreshLoop() {
        refreshTask?.cancel()
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                // Sleep first: avoids a tight retry loop (fail fast → no delay → allocate → repeat)
                // when the daemon is unreachable at the time the view appears.
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled, let self else { break }
                await self.refreshTabs()
            }
        }
    }

    /// Stop auto-refresh (call when view disappears)
    public func stopRefreshLoop() {
        refreshTask?.cancel()
    }

    // MARK: - Private Helpers

    private func updateActiveState() {
        for index in tabs.indices {
            tabs[index].isActive = tabs[index].id == activeTabId
        }
    }

    private func setupPTYHandlers(sessionId: String) {
        client.subscribePTY(
            sessionId: sessionId,
            onData: { text in
                Task { @MainActor in
                    TerminalRegistry.shared.appendOutput(text, for: sessionId)
                }
            },
            onExit: { [weak self] _ in
                Task { @MainActor [weak self] in
                    await self?.handleSessionExit(sessionId: sessionId)
                }
            }
        )
    }

    private func handleSessionExit(sessionId: String) async {
        client.unsubscribePTY(sessionId: sessionId)

        await MainActor.run {
            if let index = tabs.firstIndex(where: { $0.id == sessionId }) {
                tabs[index].isLoading = false
                tabs[index].error = "Process exited"
            }
        }
    }
}
