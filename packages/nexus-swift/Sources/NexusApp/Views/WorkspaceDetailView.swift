import AppKit
import NexusCore
import SwiftUI

// MARK: - Detail root

struct WorkspaceDetailView: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    var body: some View {
        VStack(spacing: 0) {
            SessionInfoStrip(workspace: workspace)
            Divider().overlay(Theme.separator)

            TerminalCard(workspace: workspace)
                .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                .background(Theme.bgApp)
        }
        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
        .id("workspace-detail-\(workspace.id)")
        .background(Theme.bgApp)
        .accessibilityIdentifier("workspace_detail")
        .accessibilityLabel("Workspace \(workspace.name)")
        .toolbar {
            ToolbarItem(placement: .navigation) {
                WorkspaceBreadcrumb(workspace: workspace)
            }
            // Inspector toggle — trailing icon, standard macOS style.
            ToolbarItem(placement: .primaryAction) {
                Button {
                    appState.showInspector.toggle()
                } label: {
                    Image(systemName: "sidebar.trailing")
                        .symbolRenderingMode(.hierarchical)
                }
                .help("Toggle Inspector")
            }
            ToolbarItem(placement: .primaryAction) {
                RemoteEditorOpenMenu(workspace: workspace)
            }
            // Workspace action menu (start/stop/remove) — icon adapts to state.
            ToolbarItem(placement: .primaryAction) {
                WorkspaceActionMenu(workspace: workspace)
            }
        }
    }
}

// MARK: - Open in Cursor / VS Code (Remote SSH)

private struct RemoteEditorOpenMenu: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    /// SSH pre-flight state — nil = idle, true = checking in progress.
    @State private var isCheckingSSH = false
    /// Error/status result from the last SSH check; shown as a sheet.
    @State private var sshCheckResult: SSHCheckResult? = nil

    private var sshTarget: String? {
        appState.activeDaemonProfile?.sshTarget?.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
    }

    /// Whether the workspace uses a VM backend (Firecracker or libkrun).
    private var isVMBackend: Bool {
        let b = (workspace.backend ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return b == "firecracker" || b == "libkrun"
    }

    private var isVMWorkspace: Bool {
        guard let ip = workspace.guestIp else { return false }
        return !ip.isEmpty
    }

    private var canOpen: Bool {
        guard case .connected = appState.connectionState else { return false }
        guard let sshTarget else { return false }
        return workspace.remoteSSHFolderOpen(
            jumpHost: sshTarget,
            identityFile: appState.activeDaemonProfile?.sshIdentity
        ) != nil
    }

    private var help: String {
        guard canOpen else {
            if isVMBackend && !workspace.state.isActive {
                return "Start the VM workspace first, then open it in an editor."
            }
            return "Requires a connected daemon, SSH host, and a running VM (or host repo path for process sandboxes)."
        }
        if isVMWorkspace {
            return "Open /workspace inside the VM. Runs an SSH connectivity check first."
        }
        if let port = appState.activeDaemonProfile?.sshPort, port != 22 {
            return "Open this repo on the SSH host in Cursor or VS Code. Non-default SSH ports need a matching Host in ~/.ssh/config."
        }
        return "Open this repo on the SSH host in Cursor or VS Code (Remote SSH)."
    }

    var body: some View {
        Menu {
            Button {
                Task { await open(.cursor) }
            } label: {
                Label("Open in Cursor", systemImage: "chevron.left.forwardslash.chevron.right")
            }
            Button {
                Task { await open(.vscode) }
            } label: {
                Label("Open in Visual Studio Code", systemImage: "chevron.left.forwardslash.chevron.right")
            }
            if isVMWorkspace {
                Divider()
                Button {
                    Task { await runSSHCheck() }
                } label: {
                    Label(isCheckingSSH ? "Checking SSH…" : "Check SSH Connection", systemImage: "network")
                }
                .disabled(isCheckingSSH)
            }
        } label: {
            if isCheckingSSH {
                ProgressView()
                    .scaleEffect(0.65)
                    .frame(width: 16, height: 16)
            } else {
                Image(systemName: "arrow.up.forward.square")
                    .symbolRenderingMode(.hierarchical)
            }
        }
        .menuStyle(.borderlessButton)
        .disabled(!canOpen || isCheckingSSH)
        .help(help)
        .sheet(item: $sshCheckResult) { result in
            SSHCheckResultSheet(result: result)
        }
    }

    // MARK: - Open with pre-flight

    private func open(_ app: RemoteEditorApp) async {
        guard let sshTarget else { return }
        guard let spec = workspace.remoteSSHFolderOpen(jumpHost: sshTarget, identityFile: appState.activeDaemonProfile?.sshIdentity) else { return }

        guard let _ = spec.vmGuestIP.flatMap({ $0.isEmpty ? nil : $0 }) else {
            // Non-VM workspace: open directly without SSH check.
            guard let url = RemoteEditorURLBuilder.folderURL(app: app, sshTarget: spec.sshHostForURI, absoluteRemotePath: spec.remotePath) else { return }
            NSWorkspace.shared.open(url)
            return
        }

        // For VM workspaces: delegate everything (config write + SSH check + open) to the CLI.
        // This runs as a normal process with full filesystem/network permissions.
        isCheckingSSH = true
        defer { isCheckingSSH = false }

        let result = await runCLIOpenEditor(workspaceId: workspace.id, app: app)
        if let failure = result {
            sshCheckResult = failure
        }
        // On success the CLI itself opens the editor URL via `open cursor://...`.
    }

    // MARK: - Standalone "Check SSH" action

    private func runSSHCheck() async {
        isCheckingSSH = true
        defer { isCheckingSSH = false }

        let result = await runCLIOpenEditor(workspaceId: workspace.id, app: .cursor, checkOnly: true)
        if let failure = result {
            sshCheckResult = failure
        } else {
            sshCheckResult = SSHCheckResult(
                guestIP: workspace.guestIp ?? "",
                isSuccess: true,
                summary: "SSH check passed",
                detail: "Connection from Mac → engine host → VM is working."
            )
        }
    }

    // MARK: - CLI delegate

    /// Calls AppState.openEditorViaCLI and maps the result to SSHCheckResult.
    /// Returns nil on success, or an SSHCheckResult describing the failure.
    private func runCLIOpenEditor(workspaceId: String, app: RemoteEditorApp, checkOnly: Bool = false) async -> SSHCheckResult? {
        let (ok, detail) = await appState.openEditorViaCLI(
            workspaceID: workspaceId,
            app: app.rawValue,
            checkOnly: checkOnly
        )
        guard !ok else { return nil }
        return SSHCheckResult(
            guestIP: workspace.guestIp ?? "",
            isSuccess: false,
            summary: "SSH connection failed",
            detail: detail.isEmpty ? "nexus exited with a non-zero status" : detail
        )
    }
}
// MARK: - SSH check result model

private struct SSHCheckResult: Identifiable {
    let id = UUID()
    let guestIP: String
    let isSuccess: Bool
    let summary: String
    let detail: String
}

// MARK: - SSH check result sheet

private struct SSHCheckResultSheet: View {
    let result: SSHCheckResult
    @Environment(\.dismiss) private var dismiss

    private var iconName: String { result.isSuccess ? "checkmark.circle.fill" : "exclamationmark.triangle.fill" }
    private var iconColor: Color { result.isSuccess ? .green : .orange }
    private var title: String { result.isSuccess ? "SSH Check Passed" : "Cannot Connect to VM" }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 10) {
                Image(systemName: iconName)
                    .font(.system(size: 22))
                    .foregroundColor(iconColor)
                Text(title)
                    .font(.headline)
            }

            Text(result.summary)
                .font(.body)
                .foregroundColor(.primary)
                .textSelection(.enabled)

            if !result.guestIP.isEmpty {
                HStack {
                    Text("Guest IP:").foregroundColor(.secondary)
                    Text(result.guestIP)
                        .font(.system(.body, design: .monospaced))
                        .textSelection(.enabled)
                }
            }

            if !result.detail.isEmpty {
                GroupBox("Details") {
                    ScrollView {
                        Text(result.detail)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundColor(.secondary)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(maxHeight: 160)
                }
            }

            if !result.isSuccess {
                GroupBox("Next steps") {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("1. Run 'nexus workspace ssh-vm <name> --diagnose' in a terminal to check sshd status inside the VM.")
                        Text("2. Ensure the workspace is running and the VM image has sshd installed.")
                        Text("3. If sshd is missing, rebuild the rootfs:")
                        Text("   nexus daemon implode && sudo nexus daemon start --setup")
                            .font(.system(.caption, design: .monospaced))
                            .foregroundColor(.secondary)
                    }
                    .font(.caption)
                    .foregroundColor(.secondary)
                }
            }

            HStack {
                Spacer()
                Button("Close") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(minWidth: 480)
    }
}
private extension String {
    var nilIfEmpty: String? {
        isEmpty ? nil : self
    }
}

// MARK: - Workspace action menu (toolbar)

private struct WorkspaceActionMenu: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    private var currentOp: WorkspaceOpState? { appState.workspaceOps[workspace.id] }
    private var isBusy: Bool { currentOp != nil }

    private var primaryIcon: String {
        switch workspace.state {
        case .running, .restored: "ellipsis.circle"
        case .starting: "ellipsis.circle"
        case .paused, .stopped, .created: "play.fill"
        }
    }

    var body: some View {
        Menu {
            switch workspace.state {
            case .stopped, .created:
                Button { Task { await appState.start(workspace) } } label: {
                    Label("Start", systemImage: "play.fill")
                }
                .disabled(isBusy)
            case .starting:
                Button { } label: {
                    Label("Starting…", systemImage: "ellipsis.circle")
                }
                .disabled(true)
            case .running, .restored:
                Button { Task { await appState.stop(workspace) } } label: {
                    Label("Stop", systemImage: "stop.fill")
                }
                .disabled(isBusy)
            case .paused:
                Button { Task { await appState.start(workspace) } } label: {
                    Label("Start", systemImage: "play.fill")
                }
                .disabled(isBusy)
                Button { Task { await appState.stop(workspace) } } label: {
                    Label("Stop", systemImage: "stop.fill")
                }
                .disabled(isBusy)
            }

            Divider()

            Button(role: .destructive) {
                Task { await appState.remove(workspace) }
            } label: {
                Label("Remove", systemImage: "trash")
            }
            .disabled(isBusy)
        } label: {
            if isBusy {
                ProgressView()
                    .scaleEffect(0.65)
                    .frame(width: 16, height: 16)
            } else {
                Image(systemName: primaryIcon)
                    .symbolRenderingMode(.hierarchical)
            }
        }
        .menuStyle(.borderlessButton)
    }
}

// MARK: - Breadcrumb (toolbar)

private struct WorkspaceBreadcrumb: View {
    let workspace: Workspace
    var body: some View {
        HStack(spacing: 6) {
            Text(workspace.name)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(.primary)
            Image(systemName: "chevron.right")
                .font(.system(size: 10, weight: .medium))
                .foregroundColor(Theme.labelTertiary)
            Text(workspace.branch)
                .font(.system(size: 13))
                .foregroundColor(Theme.labelSecondary)
        }
        .padding(.horizontal, 8)
    }
}

// MARK: - Session info strip

private struct SessionInfoStrip: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState
    @State private var resolvedPath: String = ""

    /// Live directory reported by the shell (OSC 7) takes precedence over the
    /// static path fetched from the daemon info.
    private var displayPath: String {
        if let live = appState.terminalDirectory, !live.isEmpty {
            return Self.canonicalSandboxPathDisplay(live)
        }
        return Self.canonicalSandboxPathDisplay(resolvedPath)
    }

    /// Daemon `repo` / `rootPath` is the Linux host mirror (e.g. `~/.local/share/nexus/mirrors/…`); the VM shell uses `/workspace`.
    private static func canonicalSandboxPathDisplay(_ raw: String) -> String {
        let t = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.contains("/nexus/mirrors/") || t.hasSuffix("/nexus/mirrors") {
            return "/workspace"
        }
        return t.replacingOccurrences(of: FileManager.default.homeDirectoryForCurrentUser.path, with: "~")
    }

    var body: some View {
        HStack(spacing: 20) {
            HStack(spacing: 4) {
                Image(systemName: "arrow.triangle.branch")
                    .font(.system(size: 10))
                    .foregroundColor(Theme.labelTertiary)
                Text(workspace.branch)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(Theme.labelSecondary)
            }

            if !workspace.detailRuntimeLine.isEmpty {
                Divider().frame(height: 12).opacity(0.5)
                HStack(spacing: 4) {
                    Image(systemName: "cpu")
                        .font(.system(size: 10))
                        .foregroundColor(Theme.labelTertiary)
                    Text(workspace.detailRuntimeLine)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundColor(Theme.labelSecondary)
                        .lineLimit(2)
                        .truncationMode(.middle)
                }
                .accessibilityLabel("Runtime \(workspace.detailRuntimeLine)")
            }

            // Resolved workspace path — live shell directory takes precedence
            if !displayPath.isEmpty {
                Divider().frame(height: 12).opacity(0.5)
                HStack(spacing: 4) {
                    Image(systemName: "folder")
                        .font(.system(size: 10))
                        .foregroundColor(Theme.labelTertiary)
                    Text(displayPath)
                        .font(.system(size: 11, design: .monospaced))
                        .foregroundColor(Theme.labelSecondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }

            let activePorts = workspace.ports.filter { $0.tunneled }
            if !activePorts.isEmpty {
                Divider().frame(height: 12).opacity(0.5)
                HStack(spacing: 4) {
                    Image(systemName: "arrow.left.arrow.right")
                        .font(.system(size: 10))
                        .foregroundColor(Theme.labelTertiary)
                    ForEach(activePorts) { port in
                        Text("\(port.port)")
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundColor(Theme.green)
                    }
                }
            }

            Spacer()
        }
        .padding(.horizontal, 16)
        .frame(height: 34)
        .background(Theme.bgContent)
        .task(id: workspace.id) {
            if let client = appState.client as? WebSocketDaemonClient,
               let info = try? await client.workspaceInfo(id: workspace.id) {
                resolvedPath = info.workspacePath
                    .replacingOccurrences(of: FileManager.default.homeDirectoryForCurrentUser.path, with: "~")
            }
        }
    }
}

// MARK: - Terminal inset card

/// The terminal lives inside a dark rounded card within the warm off-white background,
/// matching how Conductor embeds a terminal within a light window.
private struct TerminalCard: View {
    let workspace: Workspace
    @EnvironmentObject var appState: AppState

    var body: some View {
        // GeometryReader: NSViewRepresentable terminals report an intrinsic height (~500pt) unless
        // we pin them to the detail column’s bounds — otherwise the card leaves empty space below.
        GeometryReader { proxy in
            Group {
                if let client = appState.client as? WebSocketDaemonClient {
                    MultiTabTerminalView(workspace: workspace, client: client)
                } else {
                    TerminalView(workspace: workspace)
                }
            }
            .frame(width: proxy.size.width, height: proxy.size.height)
            .id("terminal-card-\(workspace.id)")
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .stroke(Color.white.opacity(0.06), lineWidth: 1)
            )
        }
        .padding(12)
        .background(Theme.bgApp)
    }
}

// MARK: - Status pill

struct StatusPill: View {
    let status: WorkspaceStatus

    var body: some View {
        HStack(spacing: 5) {
            Circle().fill(Theme.statusColor(status)).frame(width: 6)
            Text(status.displayName)
                .font(.system(size: 11.5, weight: .medium))
                .foregroundColor(Theme.labelSecondary)
        }
        .padding(.horizontal, 2)
        .padding(.vertical, 2)
        // Unified toolbars already render “glass” capsules; extra rounded fills read as oversized pills.
    }
}

// MARK: - Toolbar icon button

struct ToolbarBtn: View {
    let icon: String
    var active: Bool = false
    let action: () -> Void
    @State private var hover = false
    var body: some View {
        Button(action: action) {
            Image(systemName: icon)
                .font(.system(size: 13))
                .foregroundColor(
                    active
                        ? (hover ? Theme.accent : Theme.labelSecondary)
                        : (hover ? Theme.label : Theme.labelSecondary)
                )
                .frame(width: 28, height: 28)
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
    }
}

typealias IconButton = ToolbarBtn
