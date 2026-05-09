import AppKit
import NexusCore
import SwiftUI

// MARK: - Panel root

struct BottomPanelView: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState
    @State private var selectedTab: InspectorTab = .ports

    enum InspectorTab: String, CaseIterable {
        case ports    = "Ports"
        case vmLog    = "Log"
        case sync     = "Sync"
    }

    var body: some View {
        VStack(spacing: 0) {
            // Tab bar
            HStack(spacing: 0) {
                Button {
                    appState.showInspector = false
                } label: {
                    Image(systemName: "sidebar.trailing")
                        .font(.system(size: 12))
                        .foregroundColor(Theme.labelSecondary)
                        .frame(width: 28, height: 28)
                }
                .buttonStyle(.plain)
                .help("Collapse Inspector")

                ForEach(InspectorTab.allCases, id: \.self) { tab in
                    Button {
                        selectedTab = tab
                    } label: {
                        Text(tab.rawValue)
                            .font(.system(size: 11, weight: selectedTab == tab ? .semibold : .regular))
                            .foregroundColor(selectedTab == tab ? Theme.label : Theme.labelSecondary)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 7)
                            .frame(maxHeight: .infinity)
                    }
                    .buttonStyle(.plain)
                    .background(
                        VStack {
                            Spacer()
                            Rectangle()
                                .fill(selectedTab == tab ? Theme.accent : Color.clear)
                                .frame(height: 2)
                        }
                    )
                }
                Spacer()
            }
            .frame(height: 30)
            .background(Theme.bgContent)
            .overlay(alignment: .bottom) {
                Divider().overlay(Theme.separator)
            }

            Group {
                switch selectedTab {
                case .ports:
                    PortsPane(workspace: workspace)
                case .vmLog:
                    VMLogPane(workspace: workspace)
                case .sync:
                    SyncPane(workspace: workspace)
                }
            }
            .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
            .background(Theme.bgContent)
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
        .background(Theme.bgContent)
    }
}

// MARK: - VM Log pane

private struct VMLogPane: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState
    @State private var result: WorkspaceSerialLog = WorkspaceSerialLog()
    @State private var isLoading = false
    @State private var lastError: String?
    @State private var refreshTask: Task<Void, Never>?

    private var isVM: Bool { workspace.usesGuestVMRuntime }

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack(spacing: 8) {
                if isLoading {
                    ProgressView().scaleEffect(0.6).frame(width: 12, height: 12)
                }
                Text(result.available ? result.path : "VM Serial Log")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(Theme.labelTertiary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .frame(maxWidth: .infinity, alignment: .leading)

                if !result.lines.isEmpty {
                    Button {
                        let text = result.lines.joined(separator: "\n")
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(text, forType: .string)
                    } label: {
                        Image(systemName: "doc.on.doc")
                            .font(.system(size: 10))
                            .foregroundColor(Theme.labelSecondary)
                    }
                    .buttonStyle(.plain)
                    .help("Copy all log lines")
                }

                Button {
                    Task { await fetch() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.system(size: 10))
                        .foregroundColor(Theme.labelSecondary)
                }
                .buttonStyle(.plain)
                .help("Refresh")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .background(Theme.bgContent)
            .overlay(alignment: .bottom) {
                Divider().overlay(Theme.separator)
            }

            // Content
            if !isVM {
                VStack(spacing: 6) {
                    Image(systemName: "desktopcomputer")
                        .font(.system(size: 20, weight: .ultraLight))
                        .foregroundColor(Theme.labelTertiary)
                    Text("Serial log is available for libkrun VM workspaces")
                        .font(Theme.fontSm)
                        .foregroundColor(Theme.labelTertiary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 16)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if !result.available && !isLoading {
                VStack(spacing: 6) {
                    Image(systemName: "doc.text")
                        .font(.system(size: 20, weight: .ultraLight))
                        .foregroundColor(Theme.labelTertiary)
                    Text(workspace.state.isActive ? "No log yet" : "Start the workspace to see the serial log")
                        .font(Theme.fontSm)
                        .foregroundColor(Theme.labelTertiary)
                        .multilineTextAlignment(.center)
                    if let err = lastError {
                        Text(err)
                            .font(.system(size: 10))
                            .foregroundColor(Theme.labelTertiary)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 16)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(result.lines.enumerated()), id: \.offset) { _, line in
                                Text(line.isEmpty ? " " : line)
                                    .font(.system(size: 10, design: .monospaced))
                                    .foregroundColor(Theme.label)
                                    .textSelection(.enabled)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(.horizontal, 12)
                                    .padding(.vertical, 1)
                            }
                            Color.clear.frame(height: 1).id("bottom")
                        }
                        .padding(.vertical, 6)
                    }
                    .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                    .onChange(of: result.lines.count) {
                        withAnimation(.none) {
                            proxy.scrollTo("bottom", anchor: .bottom)
                        }
                    }
                }
            }
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
        .task(id: workspace.id) {
            await startPolling()
        }
        .onDisappear {
            refreshTask?.cancel()
            refreshTask = nil
        }
    }

    private func startPolling() async {
        refreshTask?.cancel()
        await fetch()
        refreshTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { return }
                await fetch()
            }
        }
    }

    @MainActor
    private func fetch() async {
        guard let client = appState.client as? WebSocketDaemonClient else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            result = try await client.workspaceSerialLog(workspaceId: workspace.id, lines: 300)
            lastError = nil
        } catch {
            lastError = error.localizedDescription
        }
    }
}

// MARK: - Ports column metrics

private enum PortsColumn {
    static let local: CGFloat = 58
    static let processMin: CGFloat = 110
    static let state: CGFloat = 42
    static let open: CGFloat = 14
}

// MARK: - Ports

private struct PortsPane: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    private static let tunnelActionWidth: CGFloat = 56

    private var tunnelsTitle: String {
        workspace.hasActiveTunnels ? "Tunnels Active" : "Tunnels Inactive"
    }

    @ViewBuilder
    private var tunnelActionButton: some View {
        if workspace.hasActiveTunnels {
            Button("Stop") { Task { await appState.stopTunnels(workspace) } }
                .buttonStyle(.plain)
                .font(.system(size: 10, weight: .medium))
        } else {
            Button("Start") { Task { await appState.startTunnels(workspace) } }
                .buttonStyle(.plain)
                .font(.system(size: 10, weight: .medium))
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .center, spacing: 10) {
                    Text(tunnelsTitle)
                        .font(Theme.fontSm)
                        .foregroundColor(workspace.hasActiveTunnels ? Theme.green : Theme.labelSecondary)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, alignment: .leading)

                    tunnelActionButton
                        .frame(width: Self.tunnelActionWidth, alignment: .center)
                }
                .frame(minHeight: 22)
            }
            .padding(.horizontal, 12)
            .padding(.top, 10)
            .padding(.bottom, 10)
            Divider().overlay(Theme.separator)

            if workspace.ports.isEmpty {
                Text("No detected ports")
                    .font(Theme.fontSm)
                    .foregroundColor(Theme.labelTertiary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .center)
            } else {
                ScrollView {
                    LazyVStack(spacing: 0, pinnedViews: [.sectionHeaders]) {
                        Section {
                            ForEach(workspace.ports) { p in
                                PortRow(port: p, workspace: workspace)
                                    // Suppress implicit SwiftUI transitions that cause the "flash".
                                    .animation(.none, value: p.tunneled)
                                    .animation(.none, value: p.preferred)
                                Divider().overlay(Theme.separator).padding(.leading, 12)
                            }
                        } header: {
                            HStack(alignment: .center, spacing: 0) {
                                Text("Local")
                                    .frame(width: PortsColumn.local, alignment: .leading)
                                Text("Process")
                                    .frame(minWidth: PortsColumn.processMin, maxWidth: .infinity, alignment: .leading)
                                Text("State")
                                    .frame(width: PortsColumn.state, alignment: .leading)
                                Text("")
                                    .frame(width: PortsColumn.open, alignment: .center)
                            }
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundColor(Theme.labelSecondary)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .background(Theme.bgContent)
                            .overlay(alignment: .bottom) {
                                Divider().overlay(Theme.separator)
                            }
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .topLeading)
                }
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
            }
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
    }
}

private struct PortRow: View {
    let port: ForwardedPort
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 0) {
            Text(String(port.port))
                .frame(width: PortsColumn.local, alignment: .leading)
                .font(.system(size: 11, weight: .medium, design: .monospaced))
                .foregroundColor(Theme.label)
            if let process = port.process, !process.isEmpty {
                Text(process)
                    .frame(minWidth: PortsColumn.processMin, maxWidth: .infinity, alignment: .leading)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.labelSecondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(process)
            } else {
                Text("—")
                    .frame(minWidth: PortsColumn.processMin, maxWidth: .infinity, alignment: .leading)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.labelTertiary)
            }
            Button {
                Task {
                    if port.tunneled || port.preferred {
                        await appState.removePort(port.port, workspace: workspace)
                    } else {
                        await appState.addPort(port.port, workspace: workspace)
                    }
                }
            } label: {
                HStack(spacing: 4) {
                    Circle().fill(port.tunneled ? Theme.green : Theme.labelTertiary).frame(width: 5)
                    Text(port.tunneled ? "On" : "Off")
                        .font(.system(size: 10))
                        .foregroundColor(port.tunneled ? Theme.green : Theme.labelTertiary)
                }
            }
            .frame(width: PortsColumn.state, alignment: .leading)
            .buttonStyle(.plain)
            .help(port.tunneled ? "Turn off tunnel" : "Turn on tunnel")
            Button {
                NSWorkspace.shared.open(port.localURL)
            } label: {
                Image(systemName: "arrow.up.forward.square")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(Theme.labelSecondary)
                    .frame(width: PortsColumn.open, alignment: .center)
            }
            .buttonStyle(.plain)
            .help("Open localhost:\(port.port)")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .accessibilityIdentifier("port_row_\(port.port)")
    }
}

// MARK: - Log (real git log via exec)

private struct LogPane: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState
    @State private var entries: [LogEntry] = []
    @State private var state: LoadState = .idle

    enum LoadState { case idle, loading, loaded, error(String) }

    struct LogEntry: Identifiable {
        let id = UUID()
        let hash: String
        let subject: String
        let author: String
        let date: String
    }

    var body: some View {
        Group {
            switch state {
            case .idle, .loading:
                HStack(spacing: 6) {
                    ProgressView().scaleEffect(0.7)
                    Text("Loading…").font(Theme.fontSm).foregroundColor(Theme.labelTertiary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)

            case .error(let msg):
                VStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle")
                        .foregroundColor(Theme.labelTertiary)
                    Text(msg)
                        .font(Theme.fontSm).foregroundColor(Theme.labelTertiary)
                        .multilineTextAlignment(.center).padding(.horizontal, 12)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)

            case .loaded:
                if entries.isEmpty {
                    Text("No commits found")
                        .font(Theme.fontSm).foregroundColor(Theme.labelTertiary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(entries) { entry in
                                LogRow(entry: entry)
                                Divider().overlay(Theme.separator).padding(.leading, 12)
                            }
                        }
                        .padding(.vertical, 4)
                    }
                    .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                }
            }
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
        .task(id: workspace.id) { await load() }
    }

    private func load() async {
        state = .loading
        guard let rec = LocalWorkspaceState.record(forWorkspaceID: workspace.id) else {
            state = .error(
                "No local git path for this workspace. Use the CLI or create/fork from this app so ~/.local/share/nexus/workspaces.json lists this workspace."
            )
            return
        }

        do {
            let out = try GitLogReader.recentLogLines(repoDirectory: rec.localPath, limit: 25)
            if out.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                entries = []
                state = .loaded
                return
            }
            entries = out.split(separator: "\n", omittingEmptySubsequences: true)
                .compactMap { line in
                    let parts = line.split(separator: "\t", maxSplits: 3)
                    guard parts.count >= 2 else { return nil }
                    return LogEntry(
                        hash:    String(parts[0]),
                        subject: String(parts[1]),
                        author:  parts.count > 2 ? String(parts[2]) : "",
                        date:    parts.count > 3 ? String(parts[3]) : ""
                    )
                }
            state = .loaded
        } catch {
            state = .error(error.localizedDescription)
        }
    }
}

private struct LogRow: View {
    let entry: LogPane.LogEntry
    var body: some View {
        HStack(spacing: 8) {
            Text(entry.hash)
                .font(.system(size: 10, weight: .medium, design: .monospaced))
                .foregroundColor(Theme.accent)
                .frame(width: 52, alignment: .leading)
            VStack(alignment: .leading, spacing: 1) {
                Text(entry.subject)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.label)
                    .lineLimit(1)
                if !entry.author.isEmpty || !entry.date.isEmpty {
                    HStack(spacing: 4) {
                        if !entry.author.isEmpty {
                            Text(entry.author)
                                .font(.system(size: 10))
                                .foregroundColor(Theme.labelTertiary)
                        }
                        if !entry.date.isEmpty {
                            Text("·").font(.system(size: 10)).foregroundColor(Theme.labelTertiary)
                            Text(entry.date)
                                .font(.system(size: 10))
                                .foregroundColor(Theme.labelTertiary)
                        }
                    }
                }
            }
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
    }
}

// MARK: - Sync pane

private struct SyncPane: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState
    @State private var showStartSheet = false
    @State private var localPath = ""
    @State private var refreshTask: Task<Void, Never>?

    private var allSessions: [SyncSession] {
        appState.syncSessions[workspace.id] ?? []
    }

    private var sessions: [SyncSession] {
        allSessions.filter { $0.status == "active" || $0.status == "paused" }
    }

    private var activeSession: SyncSession? {
        sessions.first { $0.status == "active" || $0.status == "paused" }
    }

    private var lastUsedPath: String {
        allSessions.last?.localPath ?? ""
    }

    private var isBusy: Bool {
        appState.syncOps[workspace.id] != nil
    }

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack(spacing: 8) {
                if isBusy {
                    ProgressView()
                        .scaleEffect(0.6)
                        .frame(width: 12, height: 12)
                }
                Text("Sync Sessions")
                    .font(Theme.fontSm)
                    .foregroundColor(Theme.labelSecondary)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)

                Button {
                    Task { await appState.refreshSyncs(workspaceID: workspace.id) }
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.system(size: 10))
                        .foregroundColor(Theme.labelSecondary)
                }
                .buttonStyle(.plain)
                .help("Refresh")
                .disabled(isBusy)

                if activeSession == nil {
                    Button("Start") {
                        if localPath.isEmpty, !lastUsedPath.isEmpty {
                            localPath = lastUsedPath
                        }
                        showStartSheet = true
                    }
                    .buttonStyle(.plain)
                    .font(.system(size: 10, weight: .medium))
                    .disabled(isBusy)
                }
            }
            .frame(minHeight: 22)
            .padding(.horizontal, 12)
            .padding(.top, 10)
            .padding(.bottom, 10)
            Divider().overlay(Theme.separator)

            // Error display
            if let error = appState.error, !error.isEmpty {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .font(.system(size: 10))
                        .foregroundColor(.red)
                    Text(error)
                        .font(.system(size: 10))
                        .foregroundColor(.red)
                        .lineLimit(2)
                    Spacer()
                    Button {
                        appState.error = nil
                    } label: {
                        Image(systemName: "xmark")
                            .font(.system(size: 10))
                            .foregroundColor(Theme.labelSecondary)
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(Color.red.opacity(0.1))
            }

            // Content
            if sessions.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: "arrow.triangle.2.circlepath")
                        .font(.system(size: 20, weight: .ultraLight))
                        .foregroundColor(Theme.labelTertiary)
                    Text("No sync sessions")
                        .font(Theme.fontSm)
                        .foregroundColor(Theme.labelTertiary)
                    Text("Start sync to keep workspace files in sync with a local path")
                        .font(.system(size: 10))
                        .foregroundColor(Theme.labelTertiary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 16)
                    Button("Start Sync") {
                        if localPath.isEmpty, !lastUsedPath.isEmpty {
                            localPath = lastUsedPath
                        }
                        showStartSheet = true
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .padding(.top, 4)
                    .disabled(isBusy)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(sessions) { session in
                            SyncSessionRow(session: session, workspace: workspace)
                            Divider().overlay(Theme.separator).padding(.leading, 12)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .topLeading)
                }
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
            }
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
        .task(id: workspace.id) {
            await startPolling()
        }
        .onAppear {
            Task {
                await appState.refreshSyncs(workspaceID: workspace.id)
            }
        }
        .onDisappear {
            refreshTask?.cancel()
            refreshTask = nil
        }
        .onChange(of: showStartSheet) { _, isShowing in
            if !isShowing {
                Task {
                    await appState.refreshSyncs(workspaceID: workspace.id)
                }
            }
        }
        .sheet(isPresented: $showStartSheet) {
            SyncStartSheet(
                workspace: workspace,
                localPath: $localPath,
                isBusy: isBusy
            )
            .environmentObject(appState)
        }
    }

    private func startPolling() async {
        refreshTask?.cancel()
        await appState.refreshSyncs(workspaceID: workspace.id)
        refreshTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { return }
                await appState.refreshSyncs(workspaceID: workspace.id)
            }
        }
    }
}

private struct SyncSessionRow: View {
    let session: SyncSession
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    private var isBusy: Bool {
        appState.syncOps[workspace.id] != nil
    }

    private var statusColor: Color {
        switch session.status {
        case "active": return Theme.green
        case "paused": return Theme.orange
        case "error": return .red
        default: return Theme.labelTertiary
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            // Status line
            HStack(spacing: 6) {
                Circle()
                    .fill(statusColor)
                    .frame(width: 6, height: 6)
                Text(session.status.capitalized)
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(statusColor)
                Spacer()
                Text(session.direction)
                    .font(.system(size: 10))
                    .foregroundColor(Theme.labelTertiary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(
                        Capsule(style: .continuous)
                            .fill(Theme.badgeMutedBg)
                    )
            }

            // Local path
            Text(session.localPath)
                .font(.system(size: 10, design: .monospaced))
                .foregroundColor(Theme.labelSecondary)
                .lineLimit(1)
                .truncationMode(.middle)

            // Stats
            if session.stats.totalSyncs > 0 {
                HStack(spacing: 12) {
                    StatItem(label: "Sent", value: formatBytes(session.stats.bytesSent))
                    StatItem(label: "Recv", value: formatBytes(session.stats.bytesReceived))
                    StatItem(label: "Files", value: "\(session.stats.filesSent + session.stats.filesReceived)")
                }
            }

            // Action buttons
            HStack(spacing: 8) {
                switch session.status {
                case "active":
                    Button("Pause") {
                        Task { await appState.pauseSync(sessionID: session.id, workspaceID: workspace.id) }
                    }
                    .buttonStyle(.plain)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundColor(Theme.accent)
                    .disabled(isBusy)

                    Button("Stop") {
                        Task { await appState.stopSync(sessionID: session.id, workspaceID: workspace.id) }
                    }
                    .buttonStyle(.plain)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundColor(.red)
                    .disabled(isBusy)

                case "paused":
                    Button("Resume") {
                        Task { await appState.resumeSync(sessionID: session.id, workspaceID: workspace.id) }
                    }
                    .buttonStyle(.plain)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundColor(Theme.green)
                    .disabled(isBusy)

                    Button("Stop") {
                        Task { await appState.stopSync(sessionID: session.id, workspaceID: workspace.id) }
                    }
                    .buttonStyle(.plain)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundColor(.red)
                    .disabled(isBusy)

                default:
                    EmptyView()
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private func formatBytes(_ bytes: Int64) -> String {
        let formatter = ByteCountFormatter()
        formatter.countStyle = .binary
        return formatter.string(fromByteCount: bytes)
    }
}

private struct StatItem: View {
    let label: String
    let value: String

    var body: some View {
        HStack(spacing: 3) {
            Text(label)
                .font(.system(size: 9))
                .foregroundColor(Theme.labelTertiary)
            Text(value)
                .font(.system(size: 9, weight: .medium))
                .foregroundColor(Theme.labelSecondary)
        }
    }
}

private struct SyncStartSheet: View {
    let workspace: Workspace
    @Binding var localPath: String
    let isBusy: Bool
    @EnvironmentObject var appState: AppState
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 16) {
            Text("Start Sync")
                .font(.system(size: 15, weight: .semibold))
                .foregroundColor(Theme.label)

            VStack(alignment: .leading, spacing: 8) {
                Text("Local Folder")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(Theme.labelSecondary)

                HStack(spacing: 8) {
                    Text(localPath.isEmpty ? "Select a folder…" : localPath)
                        .font(.system(size: 12, design: .monospaced))
                        .foregroundColor(localPath.isEmpty ? Theme.labelTertiary : Theme.label)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .frame(maxWidth: .infinity, alignment: .leading)

                    Button("Choose…") {
                        chooseFolder()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(
                    RoundedRectangle(cornerRadius: 4)
                        .stroke(Theme.separator, lineWidth: 1)
                        .background(RoundedRectangle(cornerRadius: 4).fill(Theme.bgElevated))
                )
            }

            HStack(spacing: 12) {
                Button("Cancel") {
                    dismiss()
                }
                .buttonStyle(.bordered)
                .controlSize(.small)

                Button("Start") {
                    Task {
                        await appState.startSync(
                            workspaceID: workspace.id,
                            localPath: localPath,
                            direction: "bidirectional"
                        )
                        dismiss()
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .disabled(localPath.isEmpty || isBusy)
            }
        }
        .padding(20)
        .frame(width: 400)
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.message = "Select local folder to sync with workspace"
        panel.prompt = "Select"
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = true

        if panel.runModal() == .OK, let url = panel.url {
            localPath = url.path
        }
    }
}
