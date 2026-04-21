import NexusCore
import SwiftUI

// The inspector panel shows only the Ports pane.

// MARK: - Panel root

struct BottomPanelView: View {
    let workspace: Workspace

    var body: some View {
        PortsPane(workspace: workspace)
            .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
            // Opaque surface: vibrancy behind dense tables causes ghosting on liquid glass styles.
            .background(Theme.bgContent)
    }
}

// MARK: - Ports column metrics (header + rows stay aligned; flex goes to Process)

private enum PortsColumn {
    static let local: CGFloat   = 52   // port numbers are ≤5 digits
    static let remote: CGFloat  = 52
    static let state: CGFloat   = 44   // "On"/"Off" + dot
    static let actions: CGFloat = 96   // "Add  Open ↗"
    // Process column gets remaining flex space; always at least ~80pt at min inspector width
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
            // Fixed vertical envelope + stable primary action width so “Start/Stop” and title changes do not resize the header.
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

                Text("Only one sandbox can have active tunnels at a time.")
                    .font(.system(size: 10))
                    .foregroundColor(Theme.labelTertiary)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
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
                            HStack(alignment: .center, spacing: 6) {
                                Text("Local")
                                    .frame(width: PortsColumn.local, alignment: .leading)
                                Text("Remote")
                                    .frame(width: PortsColumn.remote, alignment: .leading)
                                Text("Process")
                                    .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading)
                                    .layoutPriority(1)
                                Text("State")
                                    .frame(width: PortsColumn.state, alignment: .leading)
                        Text("Action")
                            .frame(width: PortsColumn.actions, alignment: .trailing)
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
    @State private var hover = false
    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text("\(port.port)")
                .frame(width: PortsColumn.local, alignment: .leading)
                .font(.system(size: 11, weight: .medium, design: .monospaced))
                .foregroundColor(Theme.label)
            Text("\(port.remotePort)")
                .frame(width: PortsColumn.remote, alignment: .leading)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(Theme.labelSecondary)
            if let process = port.process, !process.isEmpty {
                Text(process)
                    .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading)
                    .layoutPriority(1)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.labelSecondary)
                    .lineLimit(2)
                    .truncationMode(.tail)
                    .help(process)
            } else {
                Text("—")
                    .frame(minWidth: 0, maxWidth: .infinity, alignment: .leading)
                    .layoutPriority(1)
                    .font(.system(size: 11))
                    .foregroundColor(Theme.labelTertiary)
            }

            HStack(spacing: 4) {
                Circle().fill(port.tunneled ? Theme.green : Theme.labelTertiary).frame(width: 5)
                Text(port.tunneled ? "On" : "Off")
                    .font(.system(size: 10))
                    .foregroundColor(port.tunneled ? Theme.green : Theme.labelTertiary)
            }
            .frame(width: PortsColumn.state, alignment: .leading)

            HStack(spacing: 8) {
                if port.preferred {
                    Button("Remove") { Task { await appState.removePort(port.port, workspace: workspace) } }
                        .buttonStyle(.plain)
                        .font(.system(size: 10, weight: .medium))
                        .foregroundColor(Theme.labelSecondary)
                } else {
                    Button("Add") { Task { await appState.addPort(port.port, workspace: workspace) } }
                        .buttonStyle(.plain)
                        .font(.system(size: 10, weight: .medium))
                        .foregroundColor(Theme.accent)
                }
                Button("Open ↗") { NSWorkspace.shared.open(port.localURL) }
                    .buttonStyle(.plain)
                    .font(.system(size: 10, weight: .medium))
                    .foregroundColor(hover ? Theme.accent : Theme.labelSecondary)
                    .onHover { hover = $0 }
            }
            .frame(width: PortsColumn.actions, alignment: .trailing)
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
