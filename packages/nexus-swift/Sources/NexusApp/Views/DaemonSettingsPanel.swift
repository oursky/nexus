import NexusCore
import SwiftUI
import Foundation
import AppKit

/// Popover shown when the user clicks the connection status pill.
struct DaemonSettingsPanel: View {
    @EnvironmentObject var appState: AppState
    @State private var isCheckRunning = false
    @State private var checkResult: DaemonCheckResult?
    @State private var isConnectionLogLoading = false
    @State private var daemonLog: DaemonLogTail = DaemonLogTail()
    @State private var appLifecycleLogLines: [String] = []
    @State private var startupTraceLogLines: [String] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().opacity(0.4)
            profilesSection
            Divider().opacity(0.4)
            provisionSection
            Divider().opacity(0.4)
            healthCheckSection
            Divider().opacity(0.4)
            connectionLogSection
        }
        .frame(width: 320)
        .background(Theme.bgContent)
        .sheet(item: $checkResult) { result in
            DaemonCheckResultSheet(result: result)
        }
    }

    // MARK: - Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Circle()
                    .fill(statusColor)
                    .frame(width: 8)
                Text(statusTitle)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Theme.label)
                Spacer()
            }
            if let sub = statusSubtitle {
                Text(sub)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(Theme.labelTertiary)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(12)
    }

    // MARK: - Profiles section

    @ViewBuilder
    private var profilesSection: some View {
        VStack(alignment: .leading, spacing: 0) {
            RemoteProfileSettingsView()
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
        }
    }

    // MARK: - Provision section

    @ViewBuilder
    private var provisionSection: some View {
        VStack(alignment: .leading, spacing: 6) {
            // Live provision progress — shown while provisioning is in flight.
            if case .provisioning(let step) = appState.connectionState {
                HStack(spacing: 6) {
                    ProgressView().scaleEffect(0.65).frame(width: 14, height: 14)
                    Text(step)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundColor(Theme.labelSecondary)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(.top, 2)
            }
            HStack {
                Button {
                    appState.installDaemon()
                } label: {
                    Label(provisionButtonLabel, systemImage: provisionButtonIcon)
                }
                .buttonStyle(.borderless)
                .font(.system(size: 12))
                .foregroundColor(canProvision ? Theme.label : Theme.labelTertiary)
                .disabled(!canProvision)
                Spacer()
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .help(provisionButtonHelp)
    }

    private var canProvision: Bool {
        guard case .provisioning = appState.connectionState else {
            // Allow provisioning when offline or connected (re-provision).
            return appState.activeDaemonProfile?.sshTarget != nil
        }
        return false // already in progress
    }

    private var provisionButtonLabel: String {
        switch appState.connectionState {
        case .provisioning: return "Provisioning…"
        case .connected:    return "Re-provision Daemon"
        default:            return "Provision Daemon"
        }
    }

    private var provisionButtonIcon: String {
        switch appState.connectionState {
        case .provisioning: return "arrow.triangle.2.circlepath"
        case .connected:    return "arrow.triangle.2.circlepath"
        default:            return "bolt.fill"
        }
    }

    private var provisionButtonHelp: String {
        switch appState.connectionState {
        case .provisioning:
            return "Provisioning in progress — uploading binary, downloading rootfs, starting daemon."
        case .connected:
            return "Re-upload the daemon binary and restart it on the remote host. Use after updating the app."
        case .disconnected:
            return "Upload the daemon binary via SSH and start it on the remote host."
        default:
            return "Connect to a host profile first, then provision."
        }
    }

    // MARK: - Health check section

    @ViewBuilder
    private var healthCheckSection: some View {
        HStack {
            Button {
                Task { await runCheck() }
            } label: {
                if isCheckRunning {
                    HStack(spacing: 6) {
                        ProgressView().scaleEffect(0.7).frame(width: 14, height: 14)
                        Text("Running checks…")
                    }
                } else {
                    Label("Run Health Check", systemImage: "stethoscope")
                }
            }
            .buttonStyle(.borderless)
            .font(.system(size: 12))
            .foregroundColor(isDaemonConnected ? Theme.label : Theme.labelTertiary)
            .disabled(isCheckRunning || !isDaemonConnected)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .help(isDaemonConnected
              ? "Run nexus daemon check on the engine host to verify the full environment setup (KVM, kernel, rootfs, SSH keys, auth tokens, …)"
              : "Connect to a daemon first to run health checks.")
    }

    // MARK: - Connection log section

    @ViewBuilder
    private var connectionLogSection: some View {
        HStack {
            Button {
                Task { await fetchConnectionLogs() }
            } label: {
                if isConnectionLogLoading {
                    HStack(spacing: 6) {
                        ProgressView().scaleEffect(0.7).frame(width: 14, height: 14)
                        Text("Loading logs…")
                    }
                } else {
                    Label("View Connection Logs", systemImage: "doc.text.magnifyingglass")
                }
            }
            .buttonStyle(.borderless)
            .font(.system(size: 12))
            .foregroundColor(Theme.label)
            .disabled(isConnectionLogLoading)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .help("Show daemon log plus app lifecycle/startup traces for remote connection debugging.")
    }

    private func fetchConnectionLogs() async {
        guard !isConnectionLogLoading else { return }
        isConnectionLogLoading = true
        defer { isConnectionLogLoading = false }

        if let client = appState.client as? WebSocketDaemonClient {
            do {
                daemonLog = try await client.daemonLogTail(lines: 400)
            } catch {
                daemonLog = DaemonLogTail(lines: ["Error fetching daemon log: \(error.localizedDescription)"], path: "")
            }
        } else {
            daemonLog = DaemonLogTail(lines: ["Daemon is not connected."], path: "")
        }

        appLifecycleLogLines = tailLocalFile(named: "nexusapp.log", lines: 400)
        startupTraceLogLines = tailLocalFile(named: "app-startup-trace.log", lines: 400)
        await MainActor.run {
            ConnectionLogWindow.present(
                daemonLog: daemonLog,
                lifecycleLines: appLifecycleLogLines,
                startupLines: startupTraceLogLines
            )
        }
    }

    private func tailLocalFile(named filename: String, lines: Int) -> [String] {
        let path = (NSHomeDirectory() as NSString).appendingPathComponent(".config/nexus/run/\(filename)")
        guard let data = FileManager.default.contents(atPath: path),
              let text = String(data: data, encoding: .utf8) else {
            return ["(no \(filename) at \(path))"]
        }
        return Array(text.split(separator: "\n", omittingEmptySubsequences: false).suffix(lines)).map(String.init)
    }

    // MARK: - Actions

    private var isDaemonConnected: Bool {
        if case .running = appState.daemonStatus { return true }
        return false
    }

    private func runCheck() async {
        guard !isCheckRunning else { return }
        isCheckRunning = true
        defer { isCheckRunning = false }
        let (ok, output) = await appState.runDaemonCheckViaCLI()
        checkResult = DaemonCheckResult(id: UUID(), passed: ok, output: output)
    }

    // MARK: - Computed helpers

    private var statusColor: Color {
        switch appState.daemonStatus {
        case .running:              return Theme.green
        case .outdated:             return Theme.orange
        case .offline, .unknown:    return Color.gray
        }
    }

    private var statusTitle: String {
        switch appState.daemonStatus {
        case .running:  return "Daemon Running"
        case .outdated: return "Daemon Outdated"
        case .offline:  return "Daemon Offline"
        case .unknown:  return "Connecting…"
        }
    }

    private var statusSubtitle: String? {
        switch appState.daemonStatus {
        case .running(let info):
            let devNote = info.version == "0.0.0-dev" ? "  (dev build)" : ""
            return "v\(info.version)  ·  protocol \(info.protocolVersion)\(devNote)"
        case .outdated(let info):
            return "Running protocol v\(info.protocolVersion), requires v\(DaemonInfo.requiredProtocol)."
        case .offline:
            return "No remote daemon connected."
        case .unknown:
            return nil
        }
    }
}

// MARK: - Supporting types

struct DaemonCheckResult: Identifiable {
    let id: UUID
    let passed: Bool
    let output: String
}

struct DaemonCheckResultSheet: View {
    let result: DaemonCheckResult
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Image(systemName: result.passed ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                    .foregroundColor(result.passed ? Theme.green : Theme.orange)
                Text(result.passed ? "All checks passed" : "Some checks failed")
                    .font(.headline)
                Spacer()
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }

            Divider()

            ScrollView {
                Text(result.output.isEmpty ? "(no output)" : result.output)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(Theme.label)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
            }
            .frame(maxHeight: 400)
        }
        .padding(16)
        .frame(width: 520)
        .background(Theme.bgContent)
    }
}

// MARK: - Connection Log Sheet

struct ConnectionLogSheet: View {
    let daemonLog: DaemonLogTail
    let lifecycleLines: [String]
    let startupLines: [String]
    @Environment(\.dismiss) private var dismiss
    @State private var tab: LogTab = .daemon

    enum LogTab: String, CaseIterable {
        case daemon = "Daemon"
        case lifecycle = "Lifecycle"
        case startup = "Startup"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Image(systemName: "doc.text")
                    .foregroundColor(Theme.labelSecondary)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Connection Logs")
                        .font(.headline)
                    if !daemonLog.path.isEmpty {
                        Text(daemonLog.path)
                            .font(.system(size: 10, design: .monospaced))
                            .foregroundColor(Theme.labelTertiary)
                    }
                }
                Spacer()
                Button {
                    let text = currentLines().joined(separator: "\n")
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(text, forType: .string)
                } label: {
                    Label("Copy", systemImage: "doc.on.doc")
                }
                .buttonStyle(.borderless)
                .font(.system(size: 12))
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }

            Divider()

            Picker("Log", selection: $tab) {
                ForEach(LogTab.allCases, id: \.self) { item in
                    Text(item.rawValue).tag(item)
                }
            }
            .pickerStyle(.segmented)

            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        if currentLines().isEmpty {
                            Text("(log is empty)")
                                .font(.system(size: 11, design: .monospaced))
                                .foregroundColor(Theme.labelTertiary)
                                .padding(8)
                        } else {
                            ForEach(Array(currentLines().enumerated()), id: \.offset) { _, line in
                                Text(line.isEmpty ? " " : line)
                                    .font(.system(size: 11, design: .monospaced))
                                    .foregroundColor(Theme.label)
                                    .textSelection(.enabled)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 1)
                            }
                            Color.clear.frame(height: 1).id("end")
                        }
                    }
                }
                .onAppear {
                    proxy.scrollTo("end", anchor: .bottom)
                }
            }
            .frame(maxHeight: 480)
            .background(Color(nsColor: .textBackgroundColor).opacity(0.6))
            .clipShape(RoundedRectangle(cornerRadius: 6))
        }
        .padding(16)
        .frame(
            minWidth: 520,
            idealWidth: 640,
            maxWidth: 820,
            minHeight: 420,
            idealHeight: 520,
            maxHeight: 760
        )
        .background(Theme.bgContent)
        .onAppear {
            // Avoid opening partially off-screen when the previous sheet geometry
            // was persisted outside the current visible frame.
            DispatchQueue.main.async {
                NSApp.keyWindow?.center()
            }
        }
    }

    private func currentLines() -> [String] {
        switch tab {
        case .daemon: return daemonLog.lines
        case .lifecycle: return lifecycleLines
        case .startup: return startupLines
        }
    }
}

@MainActor
private enum ConnectionLogWindow {
    private static var window: NSWindow?
    private static var windowDelegate: ConnectionLogWindowDelegate?

    static func present(daemonLog: DaemonLogTail, lifecycleLines: [String], startupLines: [String]) {
        let view = ConnectionLogSheet(
            daemonLog: daemonLog,
            lifecycleLines: lifecycleLines,
            startupLines: startupLines
        )
        let host = NSHostingController(rootView: view)

        if let existing = window {
            existing.contentViewController = host
            placeOnVisibleScreen(existing)
            existing.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        let panel = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 760, height: 620),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        panel.title = "Connection Logs"
        panel.isReleasedWhenClosed = false
        panel.contentViewController = host
        placeOnVisibleScreen(panel)
        let delegate = ConnectionLogWindowDelegate {
            window = nil
            windowDelegate = nil
        }
        panel.delegate = delegate
        windowDelegate = delegate
        panel.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        window = panel
    }

    private static func placeOnVisibleScreen(_ window: NSWindow) {
        let currentFrame = window.frame
        let targetScreen = window.screen ?? NSScreen.main ?? NSScreen.screens.first
        guard let visible = targetScreen?.visibleFrame else {
            window.center()
            return
        }

        var nextFrame = currentFrame
        if nextFrame.width > visible.width {
            nextFrame.size.width = visible.width
        }
        if nextFrame.height > visible.height {
            nextFrame.size.height = visible.height
        }
        if nextFrame.minX < visible.minX {
            nextFrame.origin.x = visible.minX
        }
        if nextFrame.maxX > visible.maxX {
            nextFrame.origin.x = visible.maxX - nextFrame.width
        }
        if nextFrame.minY < visible.minY {
            nextFrame.origin.y = visible.minY
        }
        if nextFrame.maxY > visible.maxY {
            nextFrame.origin.y = visible.maxY - nextFrame.height
        }

        if !visible.intersects(currentFrame) {
            nextFrame.origin.x = visible.midX - (nextFrame.width / 2)
            nextFrame.origin.y = visible.midY - (nextFrame.height / 2)
        }

        window.setFrame(nextFrame, display: true)
    }
}

private final class ConnectionLogWindowDelegate: NSObject, NSWindowDelegate {
    private let onClose: () -> Void

    init(onClose: @escaping () -> Void) {
        self.onClose = onClose
    }

    func windowWillClose(_ notification: Notification) {
        onClose()
    }
}
